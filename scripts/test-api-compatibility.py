"""Reject narrowing of the published alpha schema without an explicit migration."""
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]


def compatible(old, new, path='schema'):
    errors = []
    for key in ['type', 'format', 'pattern', 'default', 'x-kubernetes-validations', 'x-kubernetes-list-type', 'x-kubernetes-list-map-keys', 'additionalProperties']:
        if key in old and not isinstance(old[key], dict) and old[key] != new.get(key):
            errors.append(path + ': changed ' + key)
    for key in ['maxLength', 'maxItems', 'maxProperties', 'maximum']:
        if key in new and (key not in old or new[key] < old[key]):
            errors.append(path + ': narrowed ' + key)
    for key in ['minLength', 'minItems', 'minProperties', 'minimum']:
        if key in new and new[key] > old.get(key, 0):
            errors.append(path + ': narrowed ' + key)
    if set(new.get('required', [])) - set(old.get('required', [])):
        errors.append(path + ': added required fields')
    if 'enum' in new and ('enum' not in old or set(old['enum']) - set(new['enum'])):
        errors.append(path + ': narrowed enum')
    for name, field in old.get('properties', {}).items():
        if name not in new.get('properties', {}):
            errors.append(path + ': removed field ' + name)
        else:
            errors.extend(compatible(field, new['properties'][name], path + '.' + name))
    for key in ['items', 'additionalProperties']:
        if isinstance(old.get(key), dict):
            if not isinstance(new.get(key), dict):
                errors.append(path + ': removed ' + key)
            else:
                errors.extend(compatible(old[key], new[key], path + '.' + key))
    return errors


class APICompatibility(unittest.TestCase):
    def test_published_api_accepts_existing_requests(self):
        for path in (ROOT / 'testdata/api-baseline').glob('*.yaml'):
            old = yaml.safe_load(path.read_text())['spec']
            new = yaml.safe_load((ROOT / 'config/crd' / path.name).read_text())['spec']
            self.assertEqual(old['group'], new['group'])
            self.assertEqual(old['names'], new['names'])
            self.assertEqual(old['scope'], new['scope'])
            versions = {v['name']: v for v in new['versions']}
            for version in old['versions']:
                self.assertIn(version['name'], versions)
                candidate = versions[version['name']]
                self.assertTrue(candidate['served'])
                self.assertEqual(version['storage'], candidate['storage'])
                self.assertEqual(version.get('subresources'), candidate.get('subresources'))
                self.assertEqual(compatible(version['schema']['openAPIV3Schema'], candidate['schema']['openAPIV3Schema']), [], path.name)

    def test_breaking_changes_are_detected(self):
        old = {'type': 'object', 'properties': {'name': {'type': 'string', 'maxLength': 63}}}
        self.assertTrue(compatible(old, {'type': 'object', 'properties': {}}))
        self.assertTrue(compatible(old, {'type': 'object', 'required': ['other'], 'properties': old['properties']}))
        self.assertTrue(compatible(old, {'type': 'object', 'properties': {'name': {'type': 'string', 'maxLength': 32}}}))
        self.assertEqual(compatible(old, {'type': 'object', 'properties': {**old['properties'], 'optional': {'type': 'string'}}}), [])


if __name__ == '__main__':
    unittest.main()
