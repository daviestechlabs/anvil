"""Execute the exact shipped CPU sources locally; cluster lifecycle remains a separate gate."""
import hashlib
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent


class CPUContract(unittest.TestCase):
    def test_retention_verification_holdout_and_recovery(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            artifacts = work / 'artifacts'
            artifacts.mkdir()
            # Sources use /tmp for Argo outputs. Redirect only output paths in this local test.
            def execute(source, environment):
                source = source.replace("Path('/tmp/", "Path('" + str(work) + '/outputs/')
                (work / 'outputs').mkdir(exist_ok=True)
                return subprocess.run(['python3', '-c', source], env={**os.environ, 'ARTIFACT_ROOT': str(artifacts), **environment}, capture_output=True, text=True, check=True)

            train = (ROOT / 'train.py').read_text()
            verify = (ROOT / 'verify.py').read_text()
            execute(train, {'WORKFLOW_UID': 'first-workflow'})
            manifest = json.loads((work / 'outputs/artifact-manifest').read_text())
            checkpoint = json.loads((work / 'outputs/checkpoint-manifest').read_text())
            content = (artifacts / manifest['uri'].removeprefix('pvc://anvil-artifacts/')).read_bytes()
            self.assertEqual(hashlib.sha256(content).hexdigest(), manifest['sha256'])
            execute(verify + '\n' + (ROOT / 'evaluate.py').read_text(), {'ARTIFACT_URI': manifest['uri'], 'ARTIFACT_SHA256': manifest['sha256']})
            report = json.loads((work / 'outputs/evaluation-report').read_text())
            self.assertTrue(report['passed'], report)
            self.assertEqual(report['artifactSHA256'], manifest['sha256'])
            execute(train, {'WORKFLOW_UID': 'recovery-workflow', 'CHECKPOINT_URI': checkpoint['uri'], 'CHECKPOINT_SHA256': checkpoint['sha256'], 'PARENT_WORKFLOW_UID': 'first-workflow'})
            self.assertEqual(json.loads((work / 'outputs/artifact-manifest').read_text()), manifest)
            with self.assertRaises(subprocess.CalledProcessError):
                execute(train, {'WORKFLOW_UID': 'replaced-workflow', 'CHECKPOINT_URI': checkpoint['uri'], 'CHECKPOINT_SHA256': checkpoint['sha256'], 'PARENT_WORKFLOW_UID': 'wrong-parent'})
            with self.assertRaises(subprocess.CalledProcessError):
                execute(verify, {'ARTIFACT_URI': manifest['uri'], 'ARTIFACT_SHA256': 'a' * 64})

    def test_preparation_is_portable_and_held(self):
        with tempfile.TemporaryDirectory() as directory:
            subprocess.run(['python3', str(ROOT / 'prepare.py'), '--namespace', 'portable-cpu', '--worker', 'portable-worker', '--storage-class', 'portable-storage', '--output', directory, '--anvilctl', os.environ.get('ANVILCTL_PATH', 'anvilctl')], check=True, capture_output=True)
            for path in Path(directory).glob('*-recipe.json'):
                recipe = json.loads(path.read_text())
                self.assertFalse(recipe['spec']['enabled'])
                self.assertEqual(recipe['metadata']['namespace'], 'portable-cpu')
                self.assertEqual(recipe['spec']['binding']['serviceAccountName'], 'portable-worker')
            claim = json.loads((Path(directory) / 'storage.json').read_text())
            self.assertEqual(claim['spec']['storageClassName'], 'portable-storage')


if __name__ == '__main__':
    unittest.main()
