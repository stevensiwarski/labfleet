"""Exercise actual role assertions and the TOML template without configuring hosts."""
import copy
import os
from pathlib import Path
import subprocess
import tempfile
import tomllib
import unittest

import yaml
from jinja2 import Environment

ROOT = Path(__file__).resolve().parents[1]


class RoleContracts(unittest.TestCase):
    def assertion(self, role, name, variables):
        tasks = yaml.safe_load((ROOT / f"roles/{role}/tasks/main.yml").read_text())
        task = copy.deepcopy(next(t for t in tasks if t["name"] == name))
        if role == "kubernetes_prereqs" and name == "Find enabled systemd swap units":
            task = {"name": "Evaluate swap query status without executing systemctl",
                    "ansible.builtin.assert": {"that": ["not (" + task["failed_when"] + ")"]}}
        self.assertIn("ansible.builtin.assert", task)
        task.pop("when", None)
        selected_tasks = [task]
        if role == "containerd" and name == "Require supported containerd version":
            selection = copy.deepcopy(next(t for t in tasks if t["name"] == "Select containerd configuration major"))
            variables = dict(variables, containerd_containerd_binary={"stat": {"exists": True}})
            selected_tasks.insert(0, selection)
        with tempfile.TemporaryDirectory(prefix="labfleet-role-test-") as tmp:
            play = Path(tmp) / "assert.yml"
            play.write_text(yaml.safe_dump([{
                "name": "Controller-only assertion fixture", "hosts": "localhost",
                "gather_facts": False, "vars": variables, "tasks": selected_tasks,
            }]))
            result = subprocess.run(
                ["ansible-playbook", "-i", "localhost,", str(play)],
                cwd=ROOT, env=dict(os.environ, ANSIBLE_CONFIG=str(ROOT / "ansible.cfg")),
                capture_output=True, text=True, timeout=20,
            )
            return result.returncode, result.stdout + result.stderr

    def test_containerd_version_guard(self):
        for version, accepted in [("1.7.24", True), ("1.7.28", True), ("2.2.1", True), ("2.2.1-0ubuntu1", True), ("2.2.3", True), ("2.0.1", False), ("3.0.0", False), ("1.6.36", False)]:
            with self.subTest(version=version):
                major = version.split(".")[0]
                rc, out = self.assertion("containerd", "Require supported containerd version", {
                    "containerd_config_major": major,
                    "containerd_containerd_version": {"stdout": f"containerd github.com/containerd/containerd {version} example"},
                })
                self.assertEqual(rc == 0, accepted, out)

    def test_containerd_plugin_health_spacing_and_failure(self):
        for status, accepted in [("ok", True), ("error", False)]:
            rc, out = self.assertion("containerd", "Require healthy containerd CRI plugin", {
                "containerd_config_major": "1",
                "containerd_containerd_plugins": {"stdout": f"io.containerd.grpc.v1    cri    linux/amd64 {status}\n"},
            })
            self.assertEqual(rc == 0, accepted, out)
        healthy = "io.containerd.cri.v1          images          -           ok\nio.containerd.cri.v1          runtime         linux/amd64 ok\n"
        for output, accepted in [(healthy, True), (healthy.replace("runtime         linux/amd64 ok", "runtime         linux/amd64 error"), False)]:
            rc, out = self.assertion("containerd", "Require healthy containerd CRI plugin", {
                "containerd_config_major": "2", "containerd_containerd_plugins": {"stdout": output},
            })
            self.assertEqual(rc == 0, accepted, out)

    def test_effective_ssh_policy_rejects_conflicts(self):
        policy = ["pubkeyauthentication yes", "passwordauthentication no", "kbdinteractiveauthentication no", "permitrootlogin no"]
        for index in [None, 0, 1, 2, 3]:
            lines = policy.copy()
            if index is not None:
                lines[index] = lines[index].rsplit(" ", 1)[0] + " conflicting-value"
            rc, out = self.assertion("ssh", "Require effective key-only non-root SSH policy", {
                "ssh_effective_policy": {"stdout_lines": lines},
            })
            self.assertEqual(rc == 0, index is None, out)

    def test_swap_query_accepts_only_exact_empty_success(self):
        for rc, stdout, stderr, accepted in [(0, "[]", "", True), (1, "[]", "", True),
                                            (2, "[]", "", False), (1, "[]", "bus failure", False),
                                            (1, "", "", False)]:
            result, out = self.assertion("kubernetes_prereqs", "Find enabled systemd swap units", {
                "kubernetes_prereqs_enabled_swap_units": {"rc": rc, "stdout": stdout, "stderr": stderr},
            })
            self.assertEqual(result == 0, accepted, out)
        for stdout, accepted in [("[]", True), ('[{"unit_file":"custom.swap","state":"enabled"}]', False)]:
            result, out = self.assertion("kubernetes_prereqs", "Require no enabled systemd swap units", {
                "kubernetes_prereqs_enabled_swap_units": {"stdout": stdout},
            })
            self.assertEqual(result == 0, accepted, out)

    def test_containerd_template_enables_cri_and_systemd_cgroups(self):
        template = Environment().from_string((ROOT / "roles/containerd/templates/config.toml.j2").read_text())
        for major, config_version, plugin in [("1", 2, "io.containerd.grpc.v1.cri"), ("2", 3, "io.containerd.cri.v1.runtime")]:
            with self.subTest(major=major):
                config = tomllib.loads(template.render(containerd_config_major=major))
                self.assertEqual(config["version"], config_version)
                self.assertNotIn("cri", config["disabled_plugins"])
                runtime = config["plugins"][plugin]["containerd"]["runtimes"]["runc"]
                self.assertEqual(runtime["runtime_type"], "io.containerd.runc.v2")
                self.assertTrue(runtime["options"]["SystemdCgroup"])
                if major == "2":
                    self.assertEqual(config["plugins"]["io.containerd.cri.v1.images"]["pinned_images"]["sandbox"], "registry.k8s.io/pause:3.10")


if __name__ == "__main__":
    unittest.main()
