import json
from pathlib import Path
import tempfile
import unittest
from workspace import compact,OWNER

class Workspace(unittest.TestCase):
    def test_only_completed_owned_workspace_is_compacted(self):
        with tempfile.TemporaryDirectory() as root:
            base=Path(root);job=base/('a'*40+'-'+'b'*12);job.mkdir()
            (job/'tree').mkdir();(job/'tree'/'source').write_text('source')
            (job/'evidence').mkdir();(job/'evidence'/'log').write_text('retained')
            (job/'owner.json').write_text(json.dumps({'owner':OWNER,'sha':'a'*40}))
            with self.assertRaises(FileNotFoundError):compact(job,base)
            self.assertTrue((job/'tree').exists())
            (job/'completed').write_text('2\n')
            (job/'modules').symlink_to(base,target_is_directory=True)
            with self.assertRaises(ValueError):compact(job,base)
            (job/'modules').unlink();compact(job,base)
            self.assertFalse((job/'tree').exists())
            self.assertEqual((job/'evidence'/'log').read_text(),'retained')
            with self.assertRaises(ValueError):compact(base,base)
