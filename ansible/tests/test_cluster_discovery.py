"""Contract tests for Issue 8's read-only Proxmox/QGA discovery."""
from pathlib import Path
import os
import subprocess
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]


class ClusterDiscoveryContracts(unittest.TestCase):
    def test_kubeconfig_is_ignored_and_export_has_private_permissions(self):
        result = subprocess.run(["git", "check-ignore", "--no-index", "issue8.kubeconfig"],
                                cwd=ROOT.parent, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0)
        plays = yaml.safe_load((ROOT / "playbooks/export-kubeconfig.yml").read_text())
        tasks = plays[0]["tasks"]
        copy = next(t for t in tasks if "ansible.builtin.copy" in t)
        self.assertEqual(copy["ansible.builtin.copy"]["mode"], "0600")
        self.assertTrue(copy["no_log"])
        self.assertFalse(copy["diff"])
        slurp = next(t for t in tasks if "ansible.builtin.slurp" in t)
        self.assertTrue(slurp["no_log"])

    def test_real_discovery_assertions_accept_only_owned_fixture(self):
        source = yaml.safe_load((ROOT / "roles/cluster_discovery/tasks/discover-one.yml").read_text())
        selected = [task for task in source if task["name"] in [
            "Validate managed VM name, role, tags, and network MACs",
            "Verify VM ownership tags and exact device MAC mapping",
        ]]
        self.assertEqual(len(selected), 2)
        node = {"name": "labfleet-cp-01", "role": "control-plane", "vm_id": 930081,
                "tags": ["labfleet", "disposable", "issue8", "control-plane"],
                "management_mac": "02:00:00:00:00:01", "mac": "02:00:00:00:00:02"}
        config = {"name": node["name"], "tags": ";".join(node["tags"]), "protection": 0,
                  "net0": "virtio=" + node["management_mac"], "net1": "virtio=" + node["mac"]}
        for override, accepted in [({}, True), ({"protection": 1}, False),
                                   ({"name": "labfleet-provisioner"}, False),
                                   ({"tags": "disposable;issue8;control-plane"}, False)]:
            with self.subTest(override=override), tempfile.TemporaryDirectory(prefix="labfleet-discovery-test-") as tmp:
                play = Path(tmp) / "test.yml"
                play.write_text(yaml.safe_dump([{"name": "Offline discovery attestation", "hosts": "localhost",
                    "gather_facts": False, "vars": {"labfleet_cluster_node": node,
                        "cluster_discovery_vm_config": {"json": {"data": dict(config, **override)}}},
                    "tasks": selected}]))
                env = dict(os.environ, ANSIBLE_CONFIG=str(ROOT / "ansible.cfg"))
                env.pop("PROXMOX_VE_API_TOKEN", None)
                env.pop("PROXMOX_VE_ENDPOINT", None)
                result = subprocess.run(["ansible-playbook", "-i", "localhost,", str(play)], cwd=ROOT,
                                        env=env, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode == 0, accepted, result.stdout + result.stderr)

    def test_discovery_is_local_bounded_and_writes_private_inventory(self):
        play = yaml.safe_load((ROOT / "playbooks/discover-cluster.yml").read_text())[0]
        self.assertEqual(play["connection"], "local")
        self.assertEqual(play["vars"]["cluster_discovery_timeout"], 900)
        tasks = play["tasks"]
        private_dir = next(task for task in tasks if task.get("name") == "Create private discovery directory")
        self.assertEqual(private_dir["ansible.builtin.file"]["mode"], "0700")
        inventory = next(task for task in tasks if task.get("name") == "Write private JSON inventory")
        self.assertEqual(inventory["ansible.builtin.copy"]["mode"], "0600")
        self.assertIn("labfleet_control_plane", str(tasks))
        self.assertIn("labfleet_workers", str(tasks))

    def test_vm_attestation_is_read_only_and_pins_qga_host_key(self):
        tasks = yaml.safe_load((ROOT / "roles/cluster_discovery/tasks/discover-one.yml").read_text())
        uri_tasks = [task["ansible.builtin.uri"] for task in tasks if "ansible.builtin.uri" in task]
        self.assertTrue(all(task["method"] in ("GET", "POST") for task in uri_tasks))
        self.assertTrue(all("validate_certs" in task for task in uri_tasks))
        self.assertTrue(any("agent/network-get-interfaces" in task["url"] for task in uri_tasks))
        exec_task = next(task for task in uri_tasks if task["method"] == "POST")
        self.assertEqual(exec_task["body"]["command"], ["/bin/cat", "/etc/ssh/ssh_host_ed25519_key.pub"])
        self.assertIn("StrictHostKeyChecking=yes", str(tasks))
        self.assertNotIn("ssh-keyscan", str(tasks))
        self.assertIn("labfleet_cluster_node.vm_id", str(tasks))
        self.assertIn("no_log", str(tasks))

    def test_fleet_cardinality_and_protective_ownership_are_enforced(self):
        play = yaml.safe_load((ROOT / "playbooks/discover-cluster.yml").read_text())[0]
        self.assertIn("930081", str(play["tasks"]))
        tasks = yaml.safe_load((ROOT / "roles/cluster_discovery/tasks/discover-one.yml").read_text())
        self.assertIn("disposable", str(tasks))
        self.assertIn("issue8", str(tasks))
        self.assertIn("management_mac", str(tasks))


if __name__ == "__main__":
    unittest.main()
