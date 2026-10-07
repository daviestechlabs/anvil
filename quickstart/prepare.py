#!/usr/bin/env python3
"""Emit a standalone, held CPU workflow without any sibling repository packages."""
import argparse
import copy
import json
import re
import subprocess
import tempfile
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--namespace', required=True)
parser.add_argument('--storage-class', default=None)
parser.add_argument('--worker', default='anvil-cpu-worker')
parser.add_argument('--node', default=None)
parser.add_argument('--anvilctl', default='anvilctl')
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
for value in [args.namespace, args.worker]:
    if not re.fullmatch(r'[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?', value):
        parser.error('Namespace and worker must be DNS labels')
args.output.mkdir(parents=True, exist_ok=True)
root = Path(__file__).resolve().parent
image = 'docker.io/library/python:3.12.13-slim-bookworm@sha256:d50fb7611f86d04a3b0471b46d7557818d88983fc3136726336b2a4c657aa30b'
security = {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}}
resources = {'requests': {'cpu': '100m', 'memory': '64Mi'}, 'limits': {'cpu': '1', 'memory': '128Mi'}}
executor = {'securityContext': {**security, 'runAsNonRoot': True}, 'resources': {'requests': {'cpu': '10m', 'memory': '32Mi'}, 'limits': {'cpu': '100m', 'memory': '128Mi'}}}
base = {'entrypoint': 'task', 'serviceAccountName': args.worker, 'parallelism': 1, 'podMetadata': {'labels': {'app.kubernetes.io/name': args.worker}}, 'securityContext': {'runAsNonRoot': True, 'runAsUser': 65532, 'runAsGroup': 65532, 'fsGroup': 65532, 'seccompProfile': {'type': 'RuntimeDefault'}}, 'automountServiceAccountToken': False, 'executor': {'serviceAccountName': args.worker}, 'podSpecPatch': json.dumps({'initContainers': [{'name': 'init', **executor}], 'containers': [{'name': 'wait', **executor}]}), 'volumes': [{'name': 'tmp', 'emptyDir': {'sizeLimit': '16Mi'}}, {'name': 'artifacts', 'persistentVolumeClaim': {'claimName': 'anvil-artifacts'}}], 'ttlStrategy': {'secondsAfterSuccess': 86400, 'secondsAfterFailure': 259200, 'secondsAfterCompletion': 604800}, 'podGC': {'strategy': 'OnWorkflowCompletion'}}
if args.node:
    base['nodeSelector'] = {'kubernetes.io/hostname': args.node}


def write(name, value):
    (args.output / name).write_text(json.dumps(value, sort_keys=True, indent=2) + '\n')


def hash_spec(command, value):
    with tempfile.NamedTemporaryFile(mode='w', suffix='.json') as file:
        json.dump(value, file)
        file.flush()
        return subprocess.check_output([args.anvilctl, command, file.name], text=True).strip()


def recipe(name, purpose, source, parameters, env, outputs, readonly):
    spec = copy.deepcopy(base)
    spec['templates'] = [{'name': 'task', 'inputs': {'parameters': [{'name': key} for key in parameters]}, 'script': {'image': image, 'command': ['python'], 'source': source, 'env': env, 'resources': resources, 'securityContext': security, 'volumeMounts': [{'name': 'tmp', 'mountPath': '/tmp'}, {'name': 'artifacts', 'mountPath': '/artifacts', 'readOnly': readonly}]}, 'outputs': {'parameters': [{'name': output, 'globalName': output, 'valueFrom': {'path': '/tmp/' + output}} for output in outputs]}}]
    template = {'apiVersion': 'argoproj.io/v1alpha1', 'kind': 'WorkflowTemplate', 'metadata': {'name': name, 'namespace': args.namespace, 'annotations': {'anvil.dev/recipe-revision': name}}, 'spec': spec}
    binding = {'revision': name, 'workflowTemplate': name, 'templateSpecSHA256': hash_spec('template-hash', template), 'serviceAccountName': args.worker, 'concurrencyKey': 'anvil-cpu', 'maxDurationSeconds': 300, 'parameters': parameters, 'outputs': outputs}
    resource = {'apiVersion': 'anvil.dev/v1alpha1', 'kind': 'TrainingRecipe', 'metadata': {'name': name, 'namespace': args.namespace, 'annotations': {'anvil.dev/purpose': purpose}}, 'spec': {'enabled': False, 'binding': binding}}
    write(name + '-template.json', template)
    write(name + '-recipe.json', resource)
    return {'name': name, 'bindingSHA256': hash_spec('recipe-hash', resource)}

sha = {'required': True, 'maxLength': 64, 'pattern': '[a-f0-9]{64}'}
uri = {'required': True, 'maxLength': 2048, 'pattern': 'pvc://anvil-artifacts/(models|checkpoints)/[a-f0-9]{64}\\.json'}
artifact_parameters = {'artifact-uri': uri, 'artifact-sha256': sha}
artifact_env = [{'name': key.upper().replace('-', '_'), 'value': '{{inputs.parameters.' + key + '}}'} for key in artifact_parameters]
train_source = (root / 'train.py').read_text()
refs = {}
refs['train'] = recipe('cpu-train-v1', 'training', train_source, {'slow': {'required': True, 'maxLength': 5, 'choices': ['true', 'false']}}, [{'name': 'WORKFLOW_UID', 'value': '{{workflow.uid}}'}, {'name': 'SLOW', 'value': '{{inputs.parameters.slow}}'}], ['artifact-manifest', 'checkpoint-manifest'], False)
verify_source = (root / 'verify.py').read_text()
refs['verify'] = recipe('cpu-verify-v1', 'artifact-verification', verify_source, artifact_parameters, artifact_env, ['verified-artifact'], True)
refs['evaluate'] = recipe('cpu-evaluate-v1', 'artifact-evaluation', verify_source + '\n' + (root / 'evaluate.py').read_text(), artifact_parameters, artifact_env, ['evaluation-report'], True)
resume_parameters = {'checkpoint-uri': uri, 'checkpoint-sha256': sha, 'parent-workflow-uid': {'required': True, 'maxLength': 128, 'pattern': '[a-zA-Z0-9-]+'}}
resume_env = [{'name': 'WORKFLOW_UID', 'value': '{{workflow.uid}}'}] + [{'name': key.upper().replace('-', '_'), 'value': '{{inputs.parameters.' + key + '}}'} for key in resume_parameters]
refs['resume'] = recipe('cpu-resume-v1', 'checkpoint-recovery', train_source, resume_parameters, resume_env, ['artifact-manifest', 'checkpoint-manifest'], False)
write('recipe-references.json', refs)
claim = {'apiVersion': 'v1', 'kind': 'PersistentVolumeClaim', 'metadata': {'name': 'anvil-artifacts', 'namespace': args.namespace}, 'spec': {'accessModes': ['ReadWriteOnce'], 'resources': {'requests': {'storage': '1Gi'}}}}
if args.storage_class is not None:
    claim['spec']['storageClassName'] = args.storage_class
write('storage.json', claim)
write('artifact-repository.json', {'apiVersion': 'v1', 'kind': 'ConfigMap', 'metadata': {'name': 'artifact-repositories', 'namespace': args.namespace, 'annotations': {'workflows.argoproj.io/default-artifact-repository': 'no-artifacts'}}, 'immutable': True, 'data': {'no-artifacts': 'archiveLogs: false\n'}})
write('worker.json', {'apiVersion': 'v1', 'kind': 'ServiceAccount', 'metadata': {'name': args.worker, 'namespace': args.namespace}})
write('worker-role.json', {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'Role', 'metadata': {'name': args.worker, 'namespace': args.namespace}, 'rules': [{'apiGroups': ['argoproj.io'], 'resources': ['workflowtaskresults'], 'verbs': ['create', 'patch']}]})
write('worker-binding.json', {'apiVersion': 'rbac.authorization.k8s.io/v1', 'kind': 'RoleBinding', 'metadata': {'name': args.worker, 'namespace': args.namespace}, 'subjects': [{'kind': 'ServiceAccount', 'name': args.worker, 'namespace': args.namespace}], 'roleRef': {'apiGroup': 'rbac.authorization.k8s.io', 'kind': 'Role', 'name': args.worker}})
write('worker-token.json', {'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': args.worker + '.service-account-token', 'namespace': args.namespace, 'annotations': {'kubernetes.io/service-account.name': args.worker}}, 'type': 'kubernetes.io/service-account-token'})
print('Held CPU manifests written to', args.output)
