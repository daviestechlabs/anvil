"""Synthetic CPU trainer. The workflow supplies bounded environment inputs."""
import hashlib
import json
import os
import time
from pathlib import Path

root = Path(os.environ.get('ARTIFACT_ROOT', '/artifacts'))
workflow_uid = os.environ['WORKFLOW_UID']
weight = bias = 0.0
start = 0
if os.environ.get('CHECKPOINT_URI'):
    uri = os.environ['CHECKPOINT_URI']
    if not uri.startswith('pvc://anvil-artifacts/checkpoints/'):
        raise ValueError('Unapproved checkpoint location')
    path = root / uri.removeprefix('pvc://anvil-artifacts/')
    content = path.read_bytes()
    if hashlib.sha256(content).hexdigest() != os.environ['CHECKPOINT_SHA256']:
        raise ValueError('Checkpoint bytes changed')
    checkpoint = json.loads(content)
    if checkpoint['workflowUID'] != os.environ['PARENT_WORKFLOW_UID']:
        raise ValueError('Checkpoint execution identity differs')
    weight, bias, start = checkpoint['weight'], checkpoint['bias'], checkpoint['step']


def retain(value, folder):
    """Publish content by hash with an atomic link that cannot replace existing bytes."""
    content = json.dumps(value, sort_keys=True).encode()
    digest = hashlib.sha256(content).hexdigest()
    directory = root / folder
    directory.mkdir(parents=True, exist_ok=True)
    temporary = directory / (workflow_uid + '.pending')
    temporary.write_bytes(content)
    destination = directory / (digest + '.json')
    try:
        os.link(temporary, destination)
    except FileExistsError:
        if destination.read_bytes() != content:
            raise ValueError('Content address collision')
    finally:
        temporary.unlink()
    return {'uri': 'pvc://anvil-artifacts/' + folder + '/' + destination.name, 'sha256': digest}


data = [(x / 10, 2 * x / 10 + 1) for x in range(10)]
checkpoint_manifest = None
for step in range(start, 200):
    dw = sum(2 * (weight * x + bias - y) * x for x, y in data) / len(data)
    db = sum(2 * (weight * x + bias - y) for x, y in data) / len(data)
    weight -= 0.1 * dw
    bias -= 0.1 * db
    if step == 99:
        checkpoint_manifest = retain({'weight': weight, 'bias': bias, 'step': step + 1, 'workflowUID': workflow_uid}, 'checkpoints')
    if os.environ.get('SLOW') == 'true':
        time.sleep(1)
manifest = retain({'weight': weight, 'bias': bias}, 'models')
Path('/tmp/artifact-manifest').write_text(json.dumps(manifest, sort_keys=True))
# Resume creates its own checkpoint after the first recovered step, with its own execution identity.
if checkpoint_manifest is None:
    checkpoint_manifest = retain({'weight': weight, 'bias': bias, 'step': 200, 'workflowUID': workflow_uid}, 'checkpoints')
Path('/tmp/checkpoint-manifest').write_text(json.dumps(checkpoint_manifest, sort_keys=True))
