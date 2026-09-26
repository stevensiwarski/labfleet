"""Render kubeadm configuration templates and lock down safe bootstrap contracts."""
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import yaml
from jinja2 import Environment, StrictUndefined

ROOT = Path(__file__).resolve().parents[1]
PINNED_VARS = yaml.safe_load((ROOT / "vars/kubernetes.yml").read_text())
VARS = {
    **PINNED_VARS,
    "kubernetes_version": "1.36.4",
    "kubernetes_minor": "1.36",
    "kubernetes_control_plane_endpoint": "192.0.2.10:6443",
    "kubernetes_node_ip": "192.0.2.11",
    "kubeadm_pod_subnet": "10.244.0.0/16",
    "kubeadm_service_subnet": "10.96.0.0/12",
    "kubeadm_containerd_socket": "unix:///run/containerd/containerd.sock",
    "kubeadm_is_control_plane": True,
    "kubeadm_control_plane_join_token": "abcdef.0123456789abcdef",
    "kubeadm_control_plane_ca_hash": "sha256:" + "a" * 64,
    "kubeadm_control_plane_certificate_key": "b" * 64,
}


class KubeadmContracts(unittest.TestCase):
    def render_docs(self, path, values=VARS):
        rendered = Environment(undefined=StrictUndefined).from_string(path.read_text()).render(**values)
        docs = list(yaml.safe_load_all(rendered))
        self.assertTrue(all(isinstance(doc, dict) for doc in docs))
        return docs

    def test_cluster_config_pins_network_endpoint_and_node_address(self):
        docs = self.render_docs(ROOT / "roles/kubeadm_control_plane/templates/cluster-config.yaml.j2")
        cluster, init, kubelet = docs
        self.assertEqual(cluster["apiVersion"], "kubeadm.k8s.io/v1beta4")
        self.assertEqual(cluster["kubernetesVersion"], "v1.36.4")
        self.assertEqual(cluster["controlPlaneEndpoint"], "192.0.2.10:6443")
        self.assertEqual(cluster["networking"]["podSubnet"], "10.244.0.0/16")
        self.assertEqual(init["localAPIEndpoint"]["advertiseAddress"], "192.0.2.11")
        args = {entry["name"]: entry["value"] for entry in init["nodeRegistration"]["kubeletExtraArgs"]}
        self.assertEqual(args["node-ip"], "192.0.2.11")
        self.assertEqual(init["nodeRegistration"]["criSocket"], "unix:///run/containerd/containerd.sock")
        self.assertEqual(kubelet["cgroupDriver"], "systemd")

    def test_worker_join_never_contains_control_plane_certificate_key(self):
        worker = self.render_docs(ROOT / "roles/kubeadm_worker/templates/join-config.yaml.j2")
        cp_values = dict(VARS, kubeadm_is_control_plane=True)
        control_plane = self.render_docs(ROOT / "roles/kubeadm_control_plane/templates/join-config.yaml.j2", cp_values)
        self.assertEqual(worker[0]["kind"], "JoinConfiguration")
        self.assertEqual(worker[0]["discovery"]["bootstrapToken"]["token"], VARS["kubeadm_control_plane_join_token"])
        self.assertNotIn("controlPlane", worker[0])
        self.assertEqual(control_plane[0]["controlPlane"]["certificateKey"], VARS["kubeadm_control_plane_certificate_key"])
        self.assertEqual(worker[1]["cgroupDriver"], "systemd")

    def test_bootstrap_roles_refuse_partial_state_and_never_reset(self):
        for role in ("kubeadm_control_plane", "kubeadm_worker"):
            text = (ROOT / f"roles/{role}/tasks/main.yml").read_text()
            self.assertNotIn("kubeadm reset", text)
            self.assertIn("kubeadm, join", text)
        self.assertIn("Incomplete Kubernetes control-plane state", (ROOT / "roles/kubeadm_control_plane/tasks/main.yml").read_text())

    def test_join_material_is_gated_by_unjoined_state_and_scrubbed_on_failure(self):
        control_plane = (ROOT / "roles/kubeadm_control_plane/tasks/main.yml").read_text()
        worker = (ROOT / "roles/kubeadm_worker/tasks/main.yml").read_text()
        self.assertIn("kubernetes_join_state.stat.exists", control_plane)
        self.assertIn("kubeadm_control_plane_unjoined_nodes | length > 0", control_plane)
        self.assertIn("always:", control_plane)
        self.assertIn("always:", worker)
        self.assertIn("no_log: true", control_plane[control_plane.index("- name: Initialize first control plane"):])
        self.assertIn("changed_when: true", control_plane[control_plane.index("- name: Upload control-plane certificates"):])
        self.assertIn("/etc/kubernetes", control_plane[control_plane.index("- name: Create Kubernetes configuration directory"):])

    def test_unjoined_decision_and_init_guard_use_role_expressions(self):
        """Exercise the role's real set_fact and init when expressions offline."""
        ansible = shutil.which("ansible-playbook")
        if not ansible:
            self.skipTest("ansible-playbook is unavailable")
        role_tasks = yaml.safe_load((ROOT / "roles/kubeadm_control_plane/tasks/main.yml").read_text())
        decision = next(task for task in role_tasks if task.get("name") == "Determine whether any nodes still need to join")
        init = next(task for task in role_tasks if task.get("name") == "Initialize first control plane")
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            inventory = tmp / "inventory.yml"
            inventory.write_text(yaml.safe_dump({"all": {"children": {
                "labfleet_control_plane": {"hosts": {"cp1": {"ansible_connection": "local"}}},
                "labfleet_nodes": {"hosts": {"cp1": {}, "worker1": {}, "cp2": {}}},
            }}}))
            playbook = tmp / "proof.yml"
            # Only the source role set_fact is executed; command tasks are never included.
            tasks = [
                {"ansible.builtin.set_fact": {"kubeadm_control_plane_state_files": {"results": [{"stat": {"exists": True}}]}}},
                decision,
                {"ansible.builtin.assert": {"that": [
                    "hostvars['cp1'].kubeadm_control_plane_unjoined_nodes | length == 0",
                    "not (" + init["when"][1] + ")",
                ]}},
                decision,
                {"ansible.builtin.assert": {"that": [
                    "hostvars['cp1'].kubeadm_control_plane_unjoined_nodes == ['worker1']",
                    "not (" + init["when"][1] + ")",
                    "hostvars['cp1'].kubeadm_control_plane_unjoined_nodes | intersect(groups['labfleet_control_plane'][1:]) | length == 0",
                ]}},
            ]
            playbook.write_text(yaml.safe_dump([
                {"hosts": "labfleet_nodes", "gather_facts": False, "tasks": [
                    {"ansible.builtin.set_fact": {"kubernetes_join_state": {"stat": {"exists": True}}}},
                ]},
                {"hosts": "labfleet_control_plane", "gather_facts": False, "tasks": tasks[:2]},
                {"hosts": "labfleet_nodes", "gather_facts": False, "tasks": [
                    {"ansible.builtin.set_fact": {"kubernetes_join_state": {"stat": {"exists": False}}}, "when": "inventory_hostname == 'worker1'"},
                ]},
                {"hosts": "labfleet_control_plane", "gather_facts": False, "tasks": tasks[2:]},
            ]))
            result = subprocess.run([ansible, "-i", str(inventory), str(playbook)], cwd=ROOT, text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_package_defaults_are_explicit_supported_pins(self):
        self.assertEqual(PINNED_VARS["kubernetes_version"], "1.36.4")
        self.assertEqual(PINNED_VARS["kubernetes_minor"], "1.36")
        self.assertEqual(PINNED_VARS["kubernetes_package_version"], "1.36.4-1.1")


if __name__ == "__main__":
    unittest.main()
