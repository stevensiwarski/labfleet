"""Contract tests for the Cilium install and cluster-validation roles."""
from pathlib import Path
import json
import unittest

import yaml
from jinja2 import Environment

ROOT = Path(__file__).resolve().parents[1]
JINJA = Environment()
JINJA.filters["to_json"] = json.dumps


class ClusterValidationContracts(unittest.TestCase):
 def test_validation_rechecks_exact_nodes_and_their_identity(self):
    tasks = yaml.safe_load((ROOT / "roles/cluster_validation/tasks/main.yml").read_text())
    names = [task.get("name", "") for task in tasks]
    self.assertIn("Re-read Kubernetes node inventory after readiness wait", names)
    ready = next(task for task in tasks if task.get("name", "").startswith("Require six Ready"))
    expression = " ".join(ready["ansible.builtin.assert"]["that"])
    self.assertIn("metadata.name", expression)
    address = next(task for task in tasks if task.get("name", "").startswith("Verify each configured"))
    expression = " ".join(address["ansible.builtin.assert"]["that"])
    self.assertIn("kubernetes_node_ip", expression)
    self.assertIn("node-role.kubernetes.io/control-plane", expression)

 def test_validation_checks_cilium_and_exact_etcd_health(self):
    tasks = yaml.safe_load((ROOT / "roles/cluster_validation/tasks/main.yml").read_text())
    names = [task.get("name", "") for task in tasks]
    self.assertIn("Run Cilium agent status on every node", names)
    self.assertIn("Check Cilium health status", names)
    self.assertIn("Check etcd endpoint health across the cluster", names)
    membership = next(task for task in tasks if task.get("name", "").startswith("Require a healthy etcd"))
    expr = " ".join(membership["ansible.builtin.assert"]["that"])
    self.assertIn("map(attribute='name')", expr)
    self.assertIn("default=false", expr)

 def test_cilium_uses_kubeadm_ipam_and_keeps_kube_proxy(self):
    values = JINJA.from_string(
        (ROOT / "roles/cilium/templates/values.yaml.j2").read_text()
    ).render(cilium_management_device="mgmt0")
    parsed = yaml.safe_load(values)
    self.assertEqual(parsed["ipam"]["mode"], "kubernetes")
    self.assertIs(parsed["kubeProxyReplacement"], False)
    self.assertEqual(parsed["routingMode"], "tunnel")
    self.assertEqual(parsed["tunnelProtocol"], "vxlan")
    self.assertEqual(parsed["devices"], ["mgmt0"])
    self.assertIs(parsed["ipv4"]["enabled"], True)
    self.assertIs(parsed["ipv6"]["enabled"], False)


 def test_cilium_artifacts_are_pinned_and_verified(self):
    defaults = yaml.safe_load((ROOT / "roles/cilium/defaults/main.yml").read_text())
    tasks = yaml.safe_load((ROOT / "roles/cilium/tasks/main.yml").read_text())
    self.assertEqual(defaults["cilium_version"], "1.20.2")
    self.assertEqual(defaults["cilium_chart_sha256"], "b2afd87b7f75f875f92a14559f14f59b7babbb479d968e3fd625a20bf30ec20e")
    checksum_tasks = [task for task in tasks if "checksum-verified" in task.get("name", "")]
    self.assertEqual(len(checksum_tasks), 2)
    self.assertTrue(all("checksum" in task["ansible.builtin.get_url"] for task in checksum_tasks))
    self.assertTrue(any("Refuse to silently change" in task.get("name", "") for task in tasks))


 def test_validation_workloads_are_scheduled_across_workers(self):
    rendered = JINJA.from_string(
        (ROOT / "roles/cluster_validation/templates/validation-pods.yaml.j2").read_text()
    ).render(
        cluster_validation_namespace="labfleet-validation",
        cluster_validation_workers=["worker-a", "worker-b"],
    )
    docs = list(yaml.safe_load_all(rendered))
    self.assertEqual(docs[0]["metadata"]["labels"]["app.kubernetes.io/managed-by"], "labfleet")
    pods = {doc["metadata"]["name"]: doc for doc in docs if doc["kind"] == "Pod"}
    self.assertEqual(pods["netcheck-server"]["spec"]["nodeSelector"]["kubernetes.io/hostname"], "worker-a")
    self.assertEqual(pods["netcheck-client"]["spec"]["nodeSelector"]["kubernetes.io/hostname"], "worker-b")


 def test_cluster_validation_refuses_unowned_namespace_and_tests_network_paths(self):
    tasks = yaml.safe_load((ROOT / "roles/cluster_validation/tasks/main.yml").read_text())
    block = next(task for task in tasks if task.get("name", "").startswith("Validate cross-node"))
    names = [task.get("name", "") for task in block["block"]]
    self.assertIn("Reject pre-existing validation namespace", names)
    self.assertIn("Remove temporary validation namespace", [task["name"] for task in block["always"]])
    connectivity = next(task for task in block["block"] if task.get("name", "").startswith("Test cross-node"))
    command = connectivity["ansible.builtin.shell"]
    self.assertIn("client_node", command)
    self.assertIn("server_node", command)
    self.assertIn("server_ip", command)
    self.assertIn("service_ip", command)
    self.assertIn("kubernetes.default.svc.cluster.local", command)
    self.assertIn("archive.ubuntu.com", command)
    cleanup = next(task for task in block["always"] if task["name"] == "Remove temporary validation namespace")
    self.assertIn("cluster_validation_created_namespace is defined", cleanup["when"])
    self.assertIn("metadata.uid", str(cleanup["when"]))
    task_names = [task.get("name", "") for task in tasks]
    self.assertIn("Read etcd membership from a control-plane member", task_names)


if __name__ == "__main__":
    unittest.main()
