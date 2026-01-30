# Steadybit Extension Scaffold

This repository contains a scaffold with a sample implementation of a [Steadybit extension](https://docs.steadybit.com/integrate-with-steadybit/extensions). You may find this repository helpful…

 - [When you want to understand what Steadybit extensions are](#understanding-the-extension-mechanism).
 - [When you want to build a Steadybit extension](#for-extension-authors)

Please follow one of the links above to move to the appropriate documentation sections.

## Understanding the Extension Mechanism

One of the best ways to understand the extension mechanism is to run an extension and experiment with its APIs. We have prepared Ona / Gitpod and GitHub codespaces setups to make this as easy as possible for you.

When you click one of these buttons, you will be directed to an online editor with a locally running extension, and the file `README.http` will open. This file contains documentation and HTTP calls you can execute to learn about extensions and this specific sample implementation.

[![Open in Ona / Gitpod](https://gitpod.io/button/open-in-gitpod.svg)](http://gitpod.io/#https://github.com/steadybit/extension-scaffold/blob/main/README.http)


[![Open in GitHub Codespaces](https://github.com/codespaces/badge.svg)](https://github.com/codespaces/new?hide_repo_select=true&ref=main&repo=595972094)


## For Extension Authors

**Note:** We recommend that you [understand the extension mechanism](#understanding-the-extension-mechanism) before following these instructions.

This repository ships with everything Steadybit extensions might need:
 - Basic usage of and initialization for ActionKit, DiscoveryKit, EventKit and ExtensionKit.
 - Extension configuration support.
 - Dockerfile and Helm chart.
 - GitHub actions for building, testing and publishing Docker images and Helm charts.
 - and more.

To use this scaffold, you need to:

 1. Get a copy of this scaffold. [Use GitHub's repository template feature](https://docs.github.com/en/repositories/creating-and-managing-repositories/creating-a-repository-from-a-template), [fork the repository](https://github.com/steadybit/extension-scaffold/fork) or [download it](https://github.com/steadybit/extension-scaffold/archive/refs/heads/main.zip).
 2. Execute `make eject` within the copy to replace the readme, license etc. files with some more appropriate starting points.
 3. Delete the `.github/workflows/cla.yml` workflow or allow access to the access for CLA verification.
 4. Rename all occurrences of `extension-scaffold` to `extension-{{other name}}`
 5. Verify that the Docker and Helm installation instructions are correct in the `README.md`
 6. Create an empty branch named "gh-pages"
 7. After the first build, ensure that you make the Docker image public through `packages -> {{your package name}} -> Package settings -> Change visibility`

### How to test this extension

You can test this extension by deploying a Steadybit agent in a local minikube cluster and connecting it to your locally running extension.

1. Start your extension locally on port 8080 with the needed environment variables, if any. For example:
	 ```bash
	 export STEADYBIT_EXTENSION_ROBOT_NAMES="robot1,robot2"
	 make run
	 ```

2. Install a Steadybit agent in minikube that connects to your on-prem or our saas Steadybit platform and points to your local extension:
   ```bash
   helm repo add steadybit https://steadybit.github.io/helm-charts
   helm repo update
   helm install steadybit-agent --namespace steadybit-agent \
     --create-namespace \
     --set agent.key=<replace-me> \
     --set agent.registerUrl=https://platform.steadybit.com \
     --set global.clusterName="minikube" \
     --set extension-container.container.runtime=docker \
     --set "agent.env[0].name=STEADYBIT_AGENT_EXTENSIONS_REGISTRATIONS_0_URL" \
     --set "agent.env[0].value=http://host.minikube.internal:8080/" \
     steadybit/steadybit-agent
   ```

The `agent.env[0].value=STEADYBIT_AGENT_EXTENSIONS_REGISTRATIONS_0_URL` is the URL of your locally running extension.
If you are using an on-prem Steadybit platform, you can set the `agent.registerUrl` to your on-prem URL. If you are using our SaaS platform, you can set it to `https://platform.steadybit.com`.

3. After successful installation, you should be able to see your extension's functionality in the Steadybit Platform.

Note: The `host.minikube.internal` domain resolves to your host machine from inside minikube, allowing the agent to access your locally running extension.

