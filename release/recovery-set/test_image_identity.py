"""A Docker-store metadata change must not excuse changed runtime contents."""
import copy
import unittest
from image_identity import verify


class ImageIdentityTests(unittest.TestCase):
    def setUp(self):
        self.source = [{'Id': 'sha256:' + 'a' * 64, 'Os': 'linux',
                        'Architecture': 'amd64',
                        'RootFS': {'Type': 'layers', 'Layers': ['sha256:' + 'b' * 64,
                                                               'sha256:' + 'c' * 64]},
                        'Config': {'User': '10001', 'Entrypoint': ['preflight'],
                                   'Cmd': [], 'Env': ['PATH=/bin']}}]

    def test_store_metadata_changes_are_allowed(self):
        loaded = copy.deepcopy(self.source)
        loaded[0].update(Id='sha256:' + 'd' * 64, GraphDriver={'Name': 'other'},
                         RepoTags=['task-only'], Metadata={'different': True})
        result = verify(self.source, loaded)
        self.assertTrue(result['runtime_contents_equal'])
        self.assertNotEqual(result['source_image'], result['recovery_image'])
        self.assertNotIn('Config', result)

    def test_any_runtime_change_refuses(self):
        for field, replacement in [('User', '0'), ('Entrypoint', ['other']),
                                   ('Cmd', ['changed']), ('Env', ['PATH=/other'])]:
            with self.subTest(field=field):
                loaded = copy.deepcopy(self.source)
                loaded[0]['Config'][field] = replacement
                with self.assertRaises(ValueError):
                    verify(self.source, loaded)
        for field, replacement in [('Os', 'other'), ('Architecture', 'arm64'),
                                   ('Id', 'task-tag')]:
            with self.subTest(field=field):
                loaded = copy.deepcopy(self.source)
                loaded[0][field] = replacement
                with self.assertRaises(ValueError):
                    verify(self.source, loaded)
        loaded = copy.deepcopy(self.source)
        loaded[0]['RootFS']['Layers'].reverse()
        with self.assertRaises(ValueError):
            verify(self.source, loaded)
        with self.assertRaises(ValueError):
            verify(self.source, [])
        with self.assertRaises(ValueError):
            verify(self.source, self.source * 2)


if __name__ == '__main__':
    unittest.main()
