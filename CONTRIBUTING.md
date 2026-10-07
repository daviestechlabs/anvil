# Contributing

Anvil is an alpha Kubernetes control plane for reviewed AI workflows.
Open an issue before changing the API or execution guarantees.
Use small pull requests with a clear problem statement and validation results.
Maintainers review changes before merge.
No contributor license agreement is required.

## Local checks

Install Go 1.26.5 or later, Python 3.11 or later, PyYAML, and Helm.
Run the commands in the root README.
Controller changes need race tests and local Kubernetes API tests.
CRD changes need schema compatibility tests and matching chart copies.
Execution changes need the disposable CPU lifecycle test.
Use synthetic data and a disposable cluster.
Never run contribution tests against production.

## Review rules

Preserve immutable execution identity and fail-closed evidence checks.
Keep worker privileges separate from operator privileges.
Promotion records approval; serving activation remains a separate action.
Document API changes and resource retention behavior.
Include tests for changed failure and recovery behavior.
Do not commit credentials, private endpoints, datasets, model weights, or production receipts.
The CI secret scanner supplements manual review.

## Community

Treat contributors with respect.
Discuss technical decisions without personal attacks or harassment.
Maintainers can remove abusive content and restrict participation.
Use GitHub issues for ordinary support.
Use the security reporting route for vulnerabilities.
