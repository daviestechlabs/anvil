"""Evaluate the exact retained model against a fixed synthetic holdout."""
# ruff: noqa: F821
# The preparation tool prefixes this source with the verifier source.
model = json.loads(content)
holdout = [(x / 10, 2 * x / 10 + 1) for x in range(10, 20)]
mse = sum((model['weight'] * x + model['bias'] - y) ** 2 for x, y in holdout) / len(holdout)
Path('/tmp/evaluation-report').write_text(json.dumps({'artifactSHA256': digest, 'passed': mse < 0.01, 'metrics': {'mse': mse}}, sort_keys=True))
