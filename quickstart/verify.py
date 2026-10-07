"""Read retained bytes and attest the requested content address."""
import hashlib
import json
import os
from pathlib import Path, PurePosixPath

uri = os.environ['ARTIFACT_URI']
prefix = 'pvc://anvil-artifacts/'
if not uri.startswith(prefix):
    raise ValueError('Artifact is outside the reviewed PVC')
relative = PurePosixPath(uri.removeprefix(prefix))
if relative.is_absolute() or '..' in relative.parts or relative.parts[0] not in ('models', 'checkpoints'):
    raise ValueError('Artifact path is outside quarantine')
path = Path(os.environ.get('ARTIFACT_ROOT', '/artifacts')) / relative
content = path.read_bytes()
digest = hashlib.sha256(content).hexdigest()
if digest != os.environ['ARTIFACT_SHA256'] or path.name != digest + '.json':
    raise ValueError('Artifact bytes do not match their content address')
Path('/tmp/verified-artifact').write_text(json.dumps({'uri': uri, 'sha256': digest}, sort_keys=True))
