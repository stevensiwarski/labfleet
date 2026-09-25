"""Offline tests execute the actual assertion tasks; no SSH/API calls are made."""
import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]
CONFIG = {
    "name": "labfleet-example-01",
    "tags": "labfleet;disposable",
    "smbios1": "uuid=11111111-2222-3333-4444-555555555555",
    "net0": "virtio=02:00:00:00:00:07,bridge=lfpxe4",
}
FACTS = {
    "distribution": "Ubuntu", "distribution_version": "24.04",
    "virtualization_role": "guest", "hostname": "labfleet-example-01",
    "product_uuid": "11111111-2222-3333-4444-555555555555",
    "interfaces": ["ens18"], "ens18": {"macaddress": "02:00:00:00:00:07"},
    "all_ipv4_addresses": ["192.0.2.77"],
}
VARS = {
    "ansible_host": "192.0.2.77", "ansible_user": "fleet", "ansible_connection": "ssh",
    "labfleet_disposable": True, "labfleet_vm_id": 930007,
    "labfleet_proxmox_node": "example-node", "labfleet_expected_hostname": "labfleet-example-01",
    "labfleet_api_config": CONFIG, "safety_api_response": {"json": {"data": CONFIG}},
    "ansible_facts": FACTS,
}


class SafetyTests(unittest.TestCase):
    def test_required_identity_fact_collectors_are_requested(self):
        tasks = yaml.safe_load((ROOT / "roles/safety/tasks/main.yml").read_text())
        setup = next(t["ansible.builtin.setup"] for t in tasks if "ansible.builtin.setup" in t)
        self.assertTrue({"hardware", "network", "virtual"}.issubset(setup["gather_subset"]))

    def invoke(self, stage, overrides=None, group="labfleet_nodes", full=False, extra_args=()):
        values = copy.deepcopy(VARS)
        values.update(overrides or {})
        values["fixture_facts"] = values.pop("ansible_facts")
        with tempfile.TemporaryDirectory(prefix="labfleet-ansible-") as tmp:
            inventory = Path(tmp) / "inventory.json"
            inventory.write_text(json.dumps({"all": {"children": {
                group: {"hosts": {"labfleet-example-01": values}}
            }}}))
            if full:
                play = ROOT / "playbooks/configure-node.yml"
            else:
                play = Path(tmp) / "test.yml"
                play.write_text(yaml.safe_dump([{
                    "name": "Exercise controller-only safety assertions",
                    "hosts": "all", "gather_facts": False,
                    "tasks": [{"name": "Supply synthetic facts without contacting a guest",
                               "ansible.builtin.set_fact": {"ansible_facts": "{{ fixture_facts }}"}},
                              {"name": "Check guard", "ansible.builtin.import_role": {
                        "name": "safety", "tasks_from": stage,
                    }}],
                }]))
            env = dict(os.environ, ANSIBLE_CONFIG=str(ROOT / "ansible.cfg"), ANSIBLE_NOCOLOR="1")
            # Tests never inherit real API credentials.
            env.pop("PROXMOX_VE_API_TOKEN", None)
            env.pop("PROXMOX_VE_ENDPOINT", None)
            result = subprocess.run(
                ["ansible-playbook", "-i", str(inventory), str(play), "--check", *extra_args],
                cwd=ROOT, env=env, capture_output=True, text=True, timeout=30,
            )
            return result.returncode, result.stdout + result.stderr

    def test_inventory_accepts_explicit_disposable(self):
        rc, out = self.invoke("inventory")
        self.assertEqual(rc, 0, out)
        self.assertIn("changed=0", out)

    def test_inventory_rejects_control_and_unsafe_inputs(self):
        for values in [
            {"labfleet_disposable": False}, {"labfleet_vm_id": 100},
            {"labfleet_vm_id": 930040}, {"ansible_connection": "local"},
            {"ansible_user": "root"}, {"ansible_host": "127.0.0.1"},
            {"labfleet_expected_hostname": "labfleet-provisioner"},
        ]:
            with self.subTest(values=values):
                rc, out = self.invoke("inventory", values)
                self.assertNotEqual(rc, 0, out)
                self.assertIn("changed=0", out)
        rc, out = self.invoke("inventory", group="bootstrap_control")
        self.assertNotEqual(rc, 0, out)

    def test_api_ownership_requires_independent_disposable_evidence(self):
        rc, out = self.invoke("ownership")
        self.assertEqual(rc, 0, out)
        for patch in [
            {"tags": "labfleet"}, {"tags": "disposable"},
            {"tags": "labfleet;disposable;provisioner"}, {"protection": 1},
            {"name": "agent01"}, {"smbios1": ""}, {"net0": ""},
        ]:
            with self.subTest(patch=patch):
                rc, out = self.invoke("ownership", {"labfleet_api_config": CONFIG | patch})
                self.assertNotEqual(rc, 0, out)

    def test_guest_identity_requires_uuid_mac_address_and_supported_os(self):
        rc, out = self.invoke("guest")
        self.assertEqual(rc, 0, out)
        for patch in [
            {"product_uuid": "00000000-0000-0000-0000-000000000000"},
            {"ens18": {"macaddress": "02:00:00:00:00:08"}},
            {"all_ipv4_addresses": ["192.0.2.78"]},
            {"hostname": "agent01"}, {"distribution": "Debian"},
            {"distribution_version": "22.04"}, {"virtualization_role": "host"},
        ]:
            with self.subTest(patch=patch):
                rc, out = self.invoke("guest", {"ansible_facts": FACTS | patch})
                self.assertNotEqual(rc, 0, out)

    def test_full_play_fails_before_remote_work_on_protected_identity(self):
        rc, out = self.invoke("inventory", {"labfleet_vm_id": 930040}, full=True)
        self.assertNotEqual(rc, 0, out)
        self.assertIn("changed=0", out)
        self.assertNotIn("TASK [base", out)
        self.assertNotIn("TASK [safety : Read target ownership", out)

    def test_bootstrap_only_group_is_not_a_play_target(self):
        rc, out = self.invoke("inventory", group="bootstrap_control", full=True)
        self.assertEqual(rc, 0, out)
        self.assertIn("no hosts matched", out)

    def test_skipping_always_tag_still_refuses_configuration(self):
        rc, out = self.invoke("inventory", full=True, extra_args=("--skip-tags", "always"))
        self.assertNotEqual(rc, 0, out)
        self.assertIn("Do not skip the ownership preflight", out)
        self.assertNotIn("TASK [base", out)


if __name__ == "__main__":
    unittest.main()
