"""Check the Helm bootstrap boundary and its shipped CRD contracts."""

import os
import subprocess
import unittest
from pathlib import Path

import yaml

PACKAGE = Path(__file__).resolve().parents[1]
CHART = PACKAGE / "charts/anvil-operator"
HELM = os.environ.get("HELM_PATH", "helm")
DIGEST = "sha256:" + "a" * 64


def render(*arguments):
    result = subprocess.run(
        [
            HELM,
            "template",
            "lab",
            str(CHART),
            "--namespace",
            "anvil",
            "--include-crds",
            "--api-versions", "argoproj.io/v1alpha1/Workflow",
            "--api-versions", "argoproj.io/v1alpha1/WorkflowTemplate",
            "--api-versions", "argoproj.io/v1alpha1/WorkflowTaskResult",
            "--set",
            "operatorImage.digest=" + DIGEST,
            *arguments,
        ],
        check=True,
        text=True,
        capture_output=True,
    )
    return [item for item in yaml.safe_load_all(result.stdout) if item]


class ChartTests(unittest.TestCase):

    def test_chart_is_a_bootstrap_for_the_native_controllers(self):
        objects = render()
        self.assertEqual(
            {item["kind"] for item in objects},
            {
                "Deployment",
                "ServiceAccount",
                "Role",
                "RoleBinding",
                "CustomResourceDefinition",
            },
        )
        for item in objects:
            if item["kind"] != "CustomResourceDefinition":
                self.assertIn(
                    item["metadata"]["namespace"], {"anvil", "anvil-training"}
                )
                self.assertRegex(
                    item["metadata"]["name"], r"^[a-z0-9]([a-z0-9-]*[a-z0-9])?$"
                )
        pod = next(item for item in objects if item["kind"] == "Deployment")["spec"][
            "template"
        ]["spec"]
        self.assertEqual(
            pod["containers"][0]["image"], "registry.example/anvil-operator@" + DIGEST
        )
        self.assertIn("--installation-namespace=anvil", pod["containers"][0]["args"])
        self.assertIn("--namespace=anvil-training", pod["containers"][0]["args"])
        self.assertFalse(
            any(
                item["kind"]
                in {
                    "ClusterRole",
                    "ClusterRoleBinding",
                    "Secret",
                    "PersistentVolumeClaim",
                    "Anvil",
                }
                for item in objects
            )
        )
        for role in (item for item in objects if item["kind"] == "Role"):
            for rule in role["rules"]:
                self.assertFalse(
                    {"secrets", "serviceaccounts/token", "pods/exec", "namespaces"}
                    & set(rule["resources"])
                )
                self.assertNotIn("*", rule["verbs"])
                self.assertNotIn("*", rule["resources"])
        for binding in (item for item in objects if item["kind"] == "RoleBinding"):
            self.assertTrue(
                any(
                    item["kind"] == "Role"
                    and item["metadata"]["name"] == binding["roleRef"]["name"]
                    and item["metadata"]["namespace"]
                    == binding["metadata"]["namespace"]
                    for item in objects
                )
            )
            self.assertEqual(binding["subjects"][0]["namespace"], "anvil")

    def test_training_only_mode_removes_installation_permissions(self):
        objects = render("--set", "installationController.enabled=false")
        roles = [item for item in objects if item["kind"] == "Role"]
        self.assertEqual(len(roles), 1)
        self.assertEqual(roles[0]["metadata"]["namespace"], "anvil-training")
        pod = next(item for item in objects if item["kind"] == "Deployment")["spec"][
            "template"
        ]["spec"]
        self.assertFalse(
            any(
                value.startswith("--installation-namespace=")
                for value in pod["containers"][0]["args"]
            )
        )

    def test_metrics_are_explicit_and_restrict_ingress(self):
        objects = render('--set', 'metrics.enabled=true', '--set', 'metrics.serviceMonitor=true', '--set', 'metrics.alerts=true', '--set', 'metrics.monitoringNamespace=observability')
        policy = next(item for item in objects if item['kind'] == 'NetworkPolicy')
        self.assertEqual(policy['spec']['ingress'][0]['from'][0]['namespaceSelector']['matchLabels'], {'kubernetes.io/metadata.name': 'observability'})
        self.assertEqual(policy['spec']['ingress'][0]['ports'][0]['port'], 8080)
        monitor = next(item for item in objects if item['kind'] == 'ServiceMonitor')
        service = next(item for item in objects if item['kind'] == 'Service')
        self.assertEqual(monitor['spec']['selector']['matchLabels'], service['metadata']['labels'])
        rules = next(item for item in objects if item['kind'] == 'PrometheusRule')['spec']['groups'][0]['rules']
        self.assertEqual(len(rules), 4)
        self.assertTrue(all('namespace="anvil"' in item['expr'] for item in rules))

    def test_missing_argo_api_fails_before_workload_render(self):
        apis = []
        for kind in ["Workflow", "WorkflowTemplate", "WorkflowTaskResult"]:
            result = subprocess.run(
                [HELM, "template", "lab", str(CHART), "--set", "operatorImage.digest=" + DIGEST, *apis],
                check=False, capture_output=True, text=True,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Argo prerequisite missing: argoproj.io/v1alpha1/" + kind, result.stderr)
            apis.extend(["--api-versions", "argoproj.io/v1alpha1/" + kind])

    def test_crd_copies_are_identical_and_digest_is_required(self):
        for source in (PACKAGE / "config/crd").glob("*.yaml"):
            if source.name != "kustomization.yaml":
                self.assertEqual(
                    source.read_bytes(), (CHART / "crds" / source.name).read_bytes()
                )
        result = subprocess.run(
            [HELM, "template", "lab", str(CHART),
             "--api-versions", "argoproj.io/v1alpha1/Workflow",
             "--api-versions", "argoproj.io/v1alpha1/WorkflowTemplate",
             "--api-versions", "argoproj.io/v1alpha1/WorkflowTaskResult"],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("published SHA-256", result.stderr)
        subprocess.run(
            [HELM, "lint", str(CHART), "--set", "operatorImage.digest=" + DIGEST],
            check=True,
            capture_output=True,
            text=True,
        )


if __name__ == "__main__":
    unittest.main()
