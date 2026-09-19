"""Validate startup invariants that Compose's syntax check does not enforce."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import unittest

class ComposeContractTest(unittest.TestCase):
    @unittest.skipUnless(shutil.which("docker"), "Docker CLI unavailable")
    def test_healthy_dependencies_have_probes_and_graph_is_acyclic(self):
        env={**os.environ,"DEMO_FAULT_SECRET":"unit-test-signing-key-not-a-runtime-secret"}
        output=subprocess.check_output(["docker","compose","config","--format","json"],cwd=Path(__file__).parents[1],env=env,text=True)
        services=json.loads(output)["services"]
        for name,config in services.items():
            for dependency,options in config.get("depends_on",{}).items():
                if options.get("condition")=="service_healthy":
                    self.assertTrue(services[dependency].get("healthcheck"),f"{name} waits on {dependency} without a healthcheck")
        self.assertNotIn("aiops-api",services)
        for name in ("order-service","payment-service","product-service","inventory-service"):
            self.assertTrue(services[name].get("healthcheck"),name)
        def visit(name,path):
            self.assertNotIn(name,path,"cyclic startup dependency")
            for child in services[name].get("depends_on",{}):visit(child,path+[name])
        for name in services:visit(name,[])

if __name__=="__main__":unittest.main()
