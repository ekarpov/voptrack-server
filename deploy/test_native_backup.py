"""Execute the native backup shell wrapper against isolated, synthetic commands."""
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
import unittest

SCRIPT = Path(__file__).with_name('tesserad-backup.example.sh')
MOCK = r'''#!/usr/bin/env python3
import json, os, pathlib, shutil, subprocess, sys, time
cmd = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
root = pathlib.Path(os.environ['BACKUP_TEST_ROOT'])
state = root/'state'
mode = os.environ.get('BACKUP_TEST_MODE', '')
with (root/'events').open('a') as out:
    out.write(json.dumps([cmd, args, state.read_text()])+'\n')
if cmd == 'date':
    print('2026-11-06T120000Z')
elif cmd == 'systemctl':
    action = args[0]
    if action == 'show':
        if mode == 'show-fail': sys.exit(1)
        print(state.read_text())
    elif action == 'stop':
        if mode != 'stop-still-active': state.write_text('inactive')
        if mode == 'stop-fail': sys.exit(1)
    elif action == 'start':
        if mode == 'restart-fail': sys.exit(1)
        state.write_text('inactive' if mode == 'restart-inactive' else 'active')
        for fence in (root/'data').glob('*.retired'): fence.write_bytes(b'after-restart')
    else: sys.exit(2)
elif cmd == 'tesserad':
    if state.read_text() != 'inactive': sys.exit(3)
    if mode == 'pause':
        (root/'paused').touch()
        time.sleep(30)
    archive = pathlib.Path(args[args.index('-backup')+1])
    archive.write_bytes(b'' if mode == 'empty' else b'synthetic-encrypted-archive')
    if mode == 'backup-fail': sys.exit(1)
elif cmd == 'cp':
    if mode == 'copy-fail': sys.exit(1)
    shutil.copyfile(args[-2], args[-1])
elif cmd == 'chmod':
    if mode == 'chmod-fail' and args[-1].endswith('archive.enc.retired'): sys.exit(1)
    sys.exit(subprocess.call(['/usr/bin/chmod']+args))
elif cmd == 'ln':
    if mode == 'publish-fail' and args[-1].endswith('.enc'): sys.exit(1)
    sys.exit(subprocess.call(['/usr/bin/ln']+args))
elif cmd == 'rm':
    if mode == 'prune-fail' and args[-1].endswith('old.enc'): sys.exit(1)
    sys.exit(subprocess.call(['/usr/bin/rm']+args))
else: sys.exit(2)
'''


class NativeBackup(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='voptrack-native-backup-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root/'bin'
        self.bin.mkdir()
        mock = self.bin/'mock'
        mock.write_text(MOCK)
        mock.chmod(0o755)
        for name in ('date', 'systemctl', 'tesserad', 'cp', 'chmod', 'ln', 'rm'):
            (self.bin/name).symlink_to(mock)
        self.data = self.root/'data'
        self.data.mkdir()
        self.fence = self.data/'dataset.db.retired'
        self.fence.write_bytes(b'before-restart')
        self.dest = self.root/'exports with spaces\nand newline'
        self.dest.mkdir()
        self.archive = self.dest/'tessera-2026-11-06T120000Z.enc'
        self.old = self.dest/'tessera-2020-old.enc'
        self.old.write_bytes(b'old archive')
        Path(str(self.old)+'.retired').write_bytes(b'old evidence')
        (self.root/'state').write_text('active')
        self.env = dict(os.environ, PATH=str(self.bin)+os.pathsep+os.environ['PATH'],
                        BACKUP_TEST_ROOT=str(self.root), TESSERA_CONFIG=str(self.root/'config'),
                        TESSERA_BACKUP_KEY=str(self.root/'key'), TESSERA_DATA_DIR=str(self.data),
                        TESSERA_BACKUP_DIR=str(self.dest), TESSERA_SERVICE='synthetic.service',
                        TESSERA_BACKUP_KEEP='1')

    def run_backup(self, mode='', code=None):
        script = SCRIPT
        if code is not None:
            script = self.root/'candidate.sh'
            script.write_text(code)
        return subprocess.run(['sh', str(script)], env=dict(self.env, BACKUP_TEST_MODE=mode),
                              capture_output=True, text=True, timeout=15)

    def events(self):
        path = self.root/'events'
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def assert_complete(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.archive.read_bytes(), b'synthetic-encrypted-archive')
        self.assertEqual(Path(str(self.archive)+'.retired').read_bytes(), b'before-restart')
        events = self.events()
        copy = next(i for i,e in enumerate(events) if e[0] == 'cp')
        start = next(i for i,e in enumerate(events) if e[0] == 'systemctl' and e[1][0] == 'start')
        self.assertLess(copy, start, 'retirement evidence must be copied before restarting')
        self.assertEqual(events[copy][2], 'inactive')
        links = [e for e in events if e[0] == 'ln']
        self.assertEqual([e[1][-1] for e in links],
                         [str(self.archive)+'.retired', str(self.archive)])
        prunes = [i for i,e in enumerate(events) if e[0] == 'rm' and e[1][-1] == str(self.old)]
        self.assertEqual(len(prunes), 1)
        self.assertGreater(prunes[0], start)
        self.assertEqual(events[prunes[0]][2], 'active')
        self.assertFalse(self.old.exists())
        self.assertFalse(Path(str(self.old)+'.retired').exists())
        self.assertEqual(self.archive.stat().st_mode & 0o777, 0o600)
        self.assertEqual(Path(str(self.archive)+'.retired').stat().st_mode & 0o777, 0o600)
        self.assertEqual(result.stdout, str(self.archive)+'\n')

    def assert_preserved(self, result):
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.old.exists(), 'failed export must retain earlier archive')
        self.assertEqual(Path(str(self.old)+'.retired').read_bytes(), b'old evidence')
        self.assertFalse(result.stdout, 'failed run must not report success')
        self.assertFalse([p for p in self.dest.glob('.backup.*') if p.is_dir()])

    def test_sqlite_pair_captured_before_restart_and_retention(self):
        self.assert_complete(self.run_backup())

    def test_postgres_fence_is_captured(self):
        self.fence.rename(self.data/'postgres-fence.retired')
        self.assert_complete(self.run_backup())

    def test_current_pair_survives_clock_rollback(self):
        future = self.dest/'tessera-2099-future.enc'
        self.old.rename(future)
        Path(str(self.old)+'.retired').rename(Path(str(future)+'.retired'))
        self.old = future
        self.assert_complete(self.run_backup())

    def test_retention_keeps_configured_number_of_complete_pairs(self):
        middle = self.dest/'tessera-2021-middle.enc'
        middle.write_bytes(b'middle archive')
        Path(str(middle)+'.retired').write_bytes(b'middle evidence')
        self.env['TESSERA_BACKUP_KEEP'] = '2'
        self.assert_complete(self.run_backup())
        self.assertTrue(middle.exists())
        self.assertTrue(Path(str(middle)+'.retired').exists())

    def test_export_failures_keep_previous_backups_and_restart(self):
        for mode in ('backup-fail', 'empty', 'copy-fail', 'chmod-fail', 'publish-fail', 'stop-fail'):
            with self.subTest(mode=mode):
                result = self.run_backup(mode)
                self.assert_preserved(result)
                self.assertEqual((self.root/'state').read_text(), 'active')
                self.assertFalse(self.archive.exists())
                Path(str(self.archive)+'.retired').unlink(missing_ok=True)
                self.fence.write_bytes(b'before-restart')

    def test_missing_and_ambiguous_fences_refused(self):
        for ambiguous in (False, True):
            with self.subTest(ambiguous=ambiguous):
                self.fence.unlink(missing_ok=True)
                if ambiguous:
                    self.fence.write_bytes(b'before-restart')
                    (self.data/'postgres-fence.retired').write_bytes(b'before-restart')
                self.assert_preserved(self.run_backup())
                self.assertFalse(self.archive.exists())

    def test_restart_failure_is_not_success_and_never_prunes(self):
        for mode in ('restart-fail', 'restart-inactive'):
            with self.subTest(mode=mode):
                self.assert_preserved(self.run_backup(mode))
                self.assertTrue(self.archive.exists(), 'complete pair survives restart failure')
                self.archive.unlink()
                Path(str(self.archive)+'.retired').unlink()
                (self.root/'state').write_text('active')

    def test_stop_not_confirmed_prevents_backup(self):
        self.assert_preserved(self.run_backup('stop-still-active'))
        self.assertFalse(any(e[0] == 'tesserad' for e in self.events()))

    def test_inactive_or_unreadable_service_is_not_started(self):
        for state,mode in (('inactive',''), ('active','show-fail')):
            with self.subTest(state=state, mode=mode):
                (self.root/'state').write_text(state)
                self.assert_preserved(self.run_backup(mode))
        self.assertFalse(any(e[0] == 'systemctl' and e[1][0] in ('start','stop') for e in self.events()))

    def test_invalid_retention_does_not_stop_service(self):
        for value in ('0', '-1', 'abc', '01', '1000000'):
            with self.subTest(value=value):
                self.env['TESSERA_BACKUP_KEEP'] = value
                self.assert_preserved(self.run_backup())
        self.assertFalse(self.events())

    def test_existing_output_is_never_overwritten(self):
        for path in (self.archive, Path(str(self.archive)+'.retired')):
            with self.subTest(path=path.name):
                path.write_bytes(b'existing output')
                self.assert_preserved(self.run_backup())
                self.assertEqual(path.read_bytes(), b'existing output')
                path.unlink()
        self.assertFalse(any(e[0] == 'systemctl' for e in self.events()))

    def test_incomplete_and_symlink_exports_are_not_pruned(self):
        incomplete = self.dest/'tessera-2010-incomplete.enc'
        incomplete.write_bytes(b'not a complete pair')
        link = self.dest/'tessera-2011-link.enc'
        link.symlink_to(self.old)
        Path(str(link)+'.retired').write_bytes(b'not owned pair')
        self.assert_complete(self.run_backup())
        self.assertTrue(incomplete.exists())
        self.assertTrue(link.is_symlink())
        self.assertTrue(Path(str(link)+'.retired').exists())

    def test_prune_failure_returns_failure_without_losing_new_pair(self):
        self.assert_preserved(self.run_backup('prune-fail'))
        self.assertTrue(self.archive.exists())
        self.assertEqual(Path(str(self.archive)+'.retired').read_bytes(), b'before-restart')

    def check_signal(self, sig, expected):
        process = subprocess.Popen(['sh', str(SCRIPT)],
            env=dict(self.env, BACKUP_TEST_MODE='pause'), stdout=subprocess.PIPE,
            stderr=subprocess.PIPE, text=True, start_new_session=True)
        try:
            deadline = time.monotonic()+10
            while not (self.root/'paused').exists() and time.monotonic() < deadline:
                time.sleep(0.02)
            self.assertTrue((self.root/'paused').exists())
            # A second run must fail before it can stop or restart the service.
            second = self.run_backup()
            self.assertNotEqual(second.returncode, 0)
            self.assertIn('another backup', second.stderr)
            os.killpg(process.pid, sig)
            stdout,stderr = process.communicate(timeout=10)
            self.assert_preserved(subprocess.CompletedProcess([], process.returncode, stdout, stderr))
            self.assertEqual(process.returncode, expected)
            self.assertEqual((self.root/'state').read_text(), 'active')
            self.assertEqual(sum(e[0] == 'systemctl' and e[1][0] == 'stop' for e in self.events()), 1)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.communicate()

    def test_term_exits_and_restarts_without_pruning(self):
        self.check_signal(signal.SIGTERM, 143)

    def test_int_exits_and_restarts_without_pruning(self):
        self.check_signal(signal.SIGINT, 130)

    def test_early_restart_defect_is_detected(self):
        code = SCRIPT.read_text().replace('# Capture exactly one', 'systemctl start "$SERVICE"\n# Capture exactly one', 1)
        result = self.run_backup(code=code)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(Path(str(self.archive)+'.retired').read_bytes(), b'after-restart')
        with self.assertRaises(AssertionError):
            self.assert_complete(result)

    def test_hidden_restart_failure_defect_is_detected(self):
        code = SCRIPT.read_text().replace('systemctl start "$SERVICE"', 'systemctl start "$SERVICE" || true')
        code = code.replace('[ "$(systemctl show --property=ActiveState --value "$SERVICE")" = active ] ||\n    fail \'service did not become active; operator action required\'', ':')
        result = self.run_backup('restart-fail', code=code)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.old.exists())
        with self.assertRaises(AssertionError):
            self.assert_preserved(result)


if __name__ == '__main__':
    unittest.main()
