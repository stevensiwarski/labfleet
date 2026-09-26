"""Run the real cluster assertions on synthetic data without SSH or API calls."""
import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]


class KubernetesSafety(unittest.TestCase):
    def invoke(self, *, role_override=None, issue="issue8", tags=None, ip_mismatch=False, fewer=False):
        cp, workers = {}, {}
        for i in range(6):
            role = "control-plane" if i < 3 else "worker"
            name = f"labfleet-cp-{i + 1:02}" if i < 3 else f"labfleet-worker-{i - 2:02}"
            ip = f"192.0.2.{i + 10}"
            values = {
                "ansible_connection": "local", "ansible_host": ip,
                "kubernetes_node_ip": ip, "labfleet_issue": issue,
                "labfleet_kubernetes_role": role_override or role,
                "kubernetes_control_plane_endpoint": "192.0.2.10:6443",
                "safety_api_response": {"json": {"data": {"tags": tags or f"labfleet;disposable;issue8;{role}"}}},
                "fixture_facts": {"default_ipv4": {"interface": "prov0" if ip_mismatch else "mgmt0", "address": ip},
                                  "mgmt0": {"ipv4": {"address": ip}}},
            }
            (cp if i < 3 else workers)[name] = values
        if fewer:
            workers.pop("labfleet-worker-03")
        inventory = {"all": {"children": {"labfleet_nodes": {"children": {
            "labfleet_control_plane": {"hosts": cp}, "labfleet_workers": {"hosts": workers},
        }}}}}
        source = yaml.safe_load((ROOT / "roles/kubernetes_safety/tasks/main.yml").read_text())
        assertions = [copy.deepcopy(t) for t in source if "ansible.builtin.assert" in t]
        with tempfile.TemporaryDirectory(prefix="labfleet-cluster-safety-") as tmp:
            inv, play = Path(tmp) / "inventory.json", Path(tmp) / "play.yml"
            inv.write_text(json.dumps(inventory))
            play.write_text(yaml.safe_dump([{"name": "Offline cluster guards", "hosts": "labfleet_nodes",
                "gather_facts": False, "tasks": [{"name": "Supply fixture facts", "ansible.builtin.set_fact": {
                    "ansible_facts": "{{ fixture_facts }}"}}, *assertions]}]))
            env = dict(os.environ, ANSIBLE_CONFIG=str(ROOT / "ansible.cfg"), ANSIBLE_NOCOLOR="1")
            env.pop("PROXMOX_VE_API_TOKEN", None)
            env.pop("PROXMOX_VE_ENDPOINT", None)
            result = subprocess.run(["ansible-playbook", "-i", str(inv), str(play)], cwd=ROOT,
                                    env=env, capture_output=True, text=True, timeout=40)
            return result.returncode, result.stdout + result.stderr

    def test_valid_six_node_topology(self):
        rc, out = self.invoke()
        self.assertEqual(rc, 0, out)

    def test_reject_wrong_issue_role_count_tags_and_interface(self):
        for case in [{"issue": "issue7"}, {"role_override": "worker"}, {"fewer": True},
                     {"tags": "labfleet;disposable;worker"}, {"ip_mismatch": True}]:
            with self.subTest(case=case):
                rc, out = self.invoke(**case)
                self.assertNotEqual(rc, 0, out)


if __name__ == "__main__":
    unittest.main()
