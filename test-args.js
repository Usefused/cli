const { execFileSync } = require("child_process");
try {
  execFileSync("./fused-cli", ["secret", "set", "e2e-plunk-sdk-runtime", "my-test-token", "--bucket", "testBucket"], {stdio: "inherit"});
} catch (e) { console.error(e); }
