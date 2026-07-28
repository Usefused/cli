import { execSync } from 'child_process';
const apiKey = "sk-test12345";
const tempConfigHome = "./temp-cfg";
const cliBinPath = "./fused-cli";
const runCLI = (args) => {
    return execSync(`${cliBinPath} ${args}`, { 
      env: { ...process.env, XDG_CONFIG_HOME: tempConfigHome },
      encoding: 'utf8' 
    });
};
runCLI(`config set api-key ${apiKey}`);
console.log("Config Key:", runCLI(`config get api-key`).trim());
