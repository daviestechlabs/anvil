#!/usr/bin/env python3
"""Prove the portable CPU lifecycle in a disposable namespace with real Argo workers."""
import argparse
import hashlib
import json
import re
import subprocess
import tempfile
import time
from datetime import UTC, datetime
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--namespace', required=True)
parser.add_argument('--operator-image', required=True)
parser.add_argument('--kubeconfig', required=True)
parser.add_argument('--anvilctl', default='anvilctl')
parser.add_argument('--helm', default='helm')
parser.add_argument('--storage-class')
parser.add_argument('--node')
parser.add_argument('--receipt', type=Path, required=True)
args = parser.parse_args()
if not re.fullmatch(r'[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?', args.namespace):
    parser.error('Use a disposable namespace DNS label')
if not re.fullmatch(r'[^\s]+@sha256:[a-f0-9]{64}', args.operator_image):
    parser.error('Use a published operator image digest')
root = Path(__file__).resolve().parent
package = root.parent
kube = ['kubectl', '--kubeconfig', args.kubeconfig, '--namespace', args.namespace]
receipt = {'schema': 'anvil-helm-cpu-evidence/v1', 'checks': [], 'operatorImage': args.operator_image, 'namespace': args.namespace, 'helmContainerLifecycleTested': True}


def call(command, **options):
    return subprocess.check_output(command, text=True, timeout=options.pop('timeout', 120), **options)


def get(kind, name):
    return json.loads(call(kube + ['get', kind, name, '-o', 'json']))


def apply(value):
    return json.loads(call(kube + ['create', '-f', '-', '-o', 'json'], input=json.dumps(value)))


def wait(kind, name, phase, seconds=300):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = get(kind, name)
        status = value.get('status', {})
        if status.get('phase') == phase and status.get('observedGeneration') == value['metadata']['generation']:
            return value
        if status.get('phase') == 'Failed':
            raise RuntimeError(json.dumps(value))
        time.sleep(2)
    raise TimeoutError(kind + '/' + name + ' did not reach ' + phase)


def ref(value):
    return {key: value['metadata'][key] for key in ['name', 'uid']}


def resource(kind, name, spec):
    return {'apiVersion': 'anvil.dev/v1alpha1', 'kind': kind, 'metadata': {'name': name, 'namespace': args.namespace}, 'spec': spec}


def run(name, recipe, parameters):
    return apply(resource('TrainingRun', name, {'request': {'recipeRef': recipe, 'durationSeconds': 300, 'parameters': parameters}, 'cancel': False}))


# Require an absent namespace so this test cannot adopt another installation or PVC.
exists = subprocess.run(['kubectl', '--kubeconfig', args.kubeconfig, 'get', 'namespace', args.namespace], capture_output=True, text=True, timeout=30, check=False)
if exists.returncode == 0 or 'NotFound' not in exists.stderr:
    raise RuntimeError('The disposable namespace must not exist; cluster errors cannot imply absence')
call(['kubectl', '--kubeconfig', args.kubeconfig, 'create', 'namespace', args.namespace])
# The namespace and PVC remain after failure for diagnosis. The caller owns final cleanup.
repository, digest = args.operator_image.split('@')
helm = [args.helm, '--kubeconfig', args.kubeconfig, '--namespace', args.namespace]
values = ['--set', 'installationController.enabled=false', '--set', 'trainingNamespace=' + args.namespace, '--set', 'operatorImage.repository=' + repository, '--set', 'operatorImage.digest=' + digest]
call(helm + ['install', 'cpu', str(package / 'charts/anvil-operator'), '--wait', '--timeout', '3m'] + values, timeout=240)
receipt['checks'].append('Helm container ready')
with tempfile.TemporaryDirectory(prefix='anvil-cpu-') as directory:
    prepared = Path(directory)
    prepare = ['python3', str(root / 'prepare.py'), '--namespace', args.namespace, '--anvilctl', args.anvilctl, '--output', directory]
    for key in ['storage_class', 'node']:
        if getattr(args, key):
            prepare.extend(['--' + key.replace('_', '-'), getattr(args, key)])
    call(prepare)
    for path in sorted(prepared.glob('*.json'), key=lambda path: (0 if path.name == 'worker.json' else 2 if path.name == 'worker-token.json' else 1, path.name)):
        if path.name != 'recipe-references.json':
            call(kube + ['apply', '-f', str(path)])
    refs = json.loads((prepared / 'recipe-references.json').read_text())
    for reference in refs.values():
        call(kube + ['patch', 'trainingrecipe', reference['name'], '--type=merge', '-p', '{"spec":{"enabled":true}}'])
    call([args.anvilctl, 'doctor', '--namespace', args.namespace, '--kubeconfig', args.kubeconfig])
    source = run('cpu-first', refs['train'], {'slow': 'false'})
    source = wait('trainingrun', source['metadata']['name'], 'Succeeded')
    inspected = json.loads(call([args.anvilctl, 'inspect', '--namespace', args.namespace, '--name', source['metadata']['name'], '--kubeconfig', args.kubeconfig]))
    if ref(inspected) != ref(source):
        raise RuntimeError('CLI inspection changed execution identity')
    receipt['sourceRun'] = ref(source)
    receipt['workflowRef'] = source['status']['workflowRef']
    receipt['checks'].append('Synthetic model and checkpoint completed')
    verifier = {'recipeRef': refs['verify'], 'durationSeconds': 300}
    artifact = apply(resource('TrainingArtifact', 'cpu-model', {'sourceRun': ref(source), 'verifier': verifier}))
    artifact = wait('trainingartifact', artifact['metadata']['name'], 'Verified')
    receipt['artifact'] = artifact['status']
    checkpoint = apply(resource('TrainingArtifact', 'cpu-checkpoint', {'sourceRun': ref(source), 'outputParameter': 'checkpoint-manifest', 'verifier': verifier}))
    checkpoint = wait('trainingartifact', checkpoint['metadata']['name'], 'Verified')
    receipt['checks'].append('Retained model and checkpoint bytes verified')
    recovered = run('cpu-recovery', refs['resume'], {'checkpoint-uri': checkpoint['status']['manifest']['uri'], 'checkpoint-sha256': checkpoint['status']['manifest']['sha256'], 'parent-workflow-uid': checkpoint['status']['lineage']['workflowUID']})
    recovered = wait('trainingrun', recovered['metadata']['name'], 'Succeeded')
    if json.loads(recovered['status']['outputs']['artifact-manifest']) != artifact['status']['manifest']:
        raise RuntimeError('Recovered checkpoint changed the final model')
    receipt['checks'].append('Checkpoint recovery reproduces exact model')
    evaluation = apply(resource('EvaluationRun', 'cpu-evaluation', {'artifactRef': ref(artifact), 'evaluator': {'recipeRef': refs['evaluate'], 'durationSeconds': 300}}))
    evaluation = wait('evaluationrun', evaluation['metadata']['name'], 'Completed')
    if evaluation['status']['passed'] is not True:
        raise RuntimeError('Synthetic holdout evaluation failed')
    promotion = apply(resource('ModelPromotion', 'cpu-promotion', {'artifactRef': ref(artifact), 'evaluationRef': ref(evaluation), 'decision': 'Approved', 'reason': 'Reviewed exact synthetic CPU holdout evidence'}))
    promotion = wait('modelpromotion', promotion['metadata']['name'], 'Approved')
    receipt['checks'].append('Exact artifact evaluated and explicitly approved')
    slow = run('cpu-cancel', refs['train'], {'slow': 'true'})
    slow = wait('trainingrun', slow['metadata']['name'], 'Running')
    # Prove an actual worker started, not just Workflow admission.
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        pods = json.loads(call(kube + ['get', 'pods', '-l', 'workflows.argoproj.io/workflow=' + slow['status']['workflowRef']['name'], '-o', 'json']))['items']
        if any(any(c['name'] == 'main' and 'running' in c.get('state', {}) for c in p.get('status', {}).get('containerStatuses', [])) for p in pods):
            break
        time.sleep(2)
    else:
        raise TimeoutError('Cancellation worker did not start')
    call(kube + ['patch', 'trainingrun', slow['metadata']['name'], '--type=merge', '-p', '{"spec":{"cancel":true}}'])
    cancelled = wait('trainingrun', slow['metadata']['name'], 'Cancelled')
    receipt['checks'].append('Active worker cancellation waits for exit')
    # Uninstall must not strand observation guards after terminal status is durable.
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        workflows = json.loads(call(kube + ['get', 'workflows', '-o', 'json']))['items']
        runs = json.loads(call(kube + ['get', 'trainingruns', '-o', 'json']))['items']
        guarded = any('anvil.dev/workflow-observation' in w['metadata'].get('finalizers', []) for w in workflows)
        guarded = guarded or any('anvil.dev/evidence-observation' in r['metadata'].get('finalizers', []) for r in runs)
        if not guarded:
            break
        time.sleep(2)
    else:
        raise TimeoutError('Observation guards remain before operator removal')
    call(helm + ['upgrade', 'cpu', str(package / 'charts/anvil-operator'), '--wait', '--timeout', '3m'] + values, timeout=240)
    upgraded = get('trainingrun', source['metadata']['name'])
    if ref(upgraded) != ref(source) or upgraded['status']['workflowRef'] != source['status']['workflowRef']:
        raise RuntimeError('Upgrade changed execution identity')
    call(helm + ['uninstall', 'cpu', '--wait', '--timeout', '3m'], timeout=240)
    if get('persistentvolumeclaim', 'anvil-artifacts')['status']['phase'] != 'Bound' or ref(get('trainingartifact', artifact['metadata']['name'])) != ref(artifact):
        raise RuntimeError('Removal lost retained storage or evidence')
    # Read bytes from a fresh non-token Pod after operator removal.
    reader_source = "import hashlib,os,pathlib; p=pathlib.Path('/artifacts')/os.environ['URI'].removeprefix('pvc://anvil-artifacts/'); d=hashlib.sha256(p.read_bytes()).hexdigest(); assert d==os.environ['SHA256']; print(d)"
    image = get('workflowtemplate', 'cpu-verify-v1')['spec']['templates'][0]['script']['image']
    pod = {'apiVersion': 'v1', 'kind': 'Pod', 'metadata': {'name': 'retained-reader', 'namespace': args.namespace}, 'spec': {'restartPolicy': 'Never', 'automountServiceAccountToken': False, 'securityContext': {'runAsNonRoot': True, 'runAsUser': 65532, 'runAsGroup': 65532, 'seccompProfile': {'type': 'RuntimeDefault'}}, 'containers': [{'name': 'reader', 'image': image, 'command': ['python', '-c', reader_source], 'env': [{'name': 'URI', 'value': artifact['status']['manifest']['uri']}, {'name': 'SHA256', 'value': artifact['status']['manifest']['sha256']}], 'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}}, 'resources': {'requests': {'cpu': '10m', 'memory': '32Mi'}, 'limits': {'cpu': '100m', 'memory': '128Mi'}}, 'volumeMounts': [{'name': 'artifacts', 'mountPath': '/artifacts', 'readOnly': True}]}], 'volumes': [{'name': 'artifacts', 'persistentVolumeClaim': {'claimName': 'anvil-artifacts'}}]}}
    if args.node:
        pod['spec']['nodeSelector'] = {'kubernetes.io/hostname': args.node}
    apply(pod)
    call(kube + ['wait', 'pod/retained-reader', '--for=jsonpath={.status.phase}=Succeeded', '--timeout=120s'], timeout=150)
    digest = call(kube + ['logs', 'retained-reader']).strip()
    if digest != artifact['status']['manifest']['sha256']:
        raise RuntimeError('Post-removal bytes changed')
    receipt['checks'].append('Upgrade preserves identities; removal retains verified bytes')
receipt['kubernetes'] = json.loads(call(['kubectl', '--kubeconfig', args.kubeconfig, 'version', '-o', 'json']))['serverVersion']['gitVersion']
receipt['recordedAt'] = datetime.now(UTC).isoformat()
receipt['nodes'] = [{'architecture': node['status']['nodeInfo']['architecture'], 'kubeletVersion': node['status']['nodeInfo']['kubeletVersion']} for node in json.loads(call(['kubectl', '--kubeconfig', args.kubeconfig, 'get', 'nodes', '-o', 'json']))['items']]
receipt['sourceSHA256'] = {str(path.relative_to(package)): hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(root.glob('*.py'))}
args.receipt.write_text(json.dumps(receipt, sort_keys=True, indent=2) + '\n')
print('CPU lifecycle passed:', args.receipt)
