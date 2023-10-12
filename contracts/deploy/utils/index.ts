import { HardhatRuntimeEnvironment } from "hardhat/types";
import inquirer from "inquirer";

import ERC1967Proxy from "@openzeppelin/contracts/build/contracts/ERC1967Proxy.json";
import UUPSUpgradeable from "@openzeppelin/contracts-upgradeable/build/contracts/UUPSUpgradeable.json";

/**
 * Deploy options for UUPS proxied contracts
 * @member initializer - name of the initializer function
 */
interface DeployOpts {
  /**
   * Name of the initializer function
   * @default "initialize"
   */
  initializer?: string;
}

/**
 * Returns the name of the proxy contract to avoid name collisions in `deployments`
 *
 * @param name name of the contract
 * @returns name of the proxy contract
 *
 * @example
 * getProxyName("MyContract") // "Proxy__MyContract"
 */
export function getProxyName(name: string): string {
  return `Proxy__${name}`;
}

/**
 * Deploy both a contract implementation and a proxy contract pointing to it
 *
 * @param hre Hardhat runtime environment
 * @param deployer the address of the deployer
 * @param name the contract name
 * @param args arguements passed into the contract initializer
 * @param opts deploy options
 */
export async function deployUUPSProxiedContract(
  hre: HardhatRuntimeEnvironment,
  deployer: string,
  name: string,
  args: any[],
  opts?: DeployOpts
) {
  const { ethers, upgrades, deployments, network } = hre;
  const { deploy, getOrNull } = deployments;
  const initializer = opts?.initializer ?? "initialize"; // default to "initialize"
  const deployerSigner = await ethers.getSigner(deployer);

  const Factory = await ethers.getContractFactory(name);
  // Validate if implementation is upgradeable
  await upgrades.validateImplementation(Factory, { kind: "uups" });

  const existingDeployment = await getOrNull(getProxyName(name));
  if (existingDeployment) {
    console.log(
      `Trying to upgrade proxy deployed at ${existingDeployment.address}`
    );
    const { doUpgrade } = await inquirer.prompt([
      {
        type: "confirm",
        name: "doUpgrade",
        message: "Do you want to upgrade the proxy?",
        default: false,
      },
    ]);
    if (!doUpgrade) {
      throw new Error("Aborting deployment");
    }
    await upgrades.validateUpgrade(existingDeployment.address, Factory, {
      kind: "uups",
    });
  }

  // Deploy implementation
  const impl = await deploy(name, {
    from: deployer,
    log: true,
  });

  let proxyAddress: string;

  if (existingDeployment) {
    proxyAddress = existingDeployment.address;
    const upgradeableInterface = new ethers.utils.Interface(
      UUPSUpgradeable.abi
    );
    const proxy = new ethers.Contract(
      existingDeployment.address,
      upgradeableInterface,
      deployerSigner
    );
    // Upgrade proxy
    const tx = await proxy.upgradeTo(impl.address);
    await tx.wait();
  } else {
    // Assemble initialization data data
    const initData = Factory.interface.encodeFunctionData(initializer, args);
    // Deploy proxy
    const proxy = await deploy(getProxyName(name), {
      from: deployer,
      contract: ERC1967Proxy,
      args: [impl.address, initData],
      log: true,
    });
    proxyAddress = proxy.address;
  }

  // Force import to ensure that the proxy is registered in the upgrades plugin
  if (network.live) {
    upgrades.forceImport(proxyAddress, Factory, { kind: "uups" });
  }

  console.log(`${name} Proxy:`, proxyAddress);
  console.log(
    `${name} Implementation Address`,
    await upgrades.erc1967.getImplementationAddress(proxyAddress)
  );
  console.log(
    `${name} Admin Address`,
    await upgrades.erc1967.getAdminAddress(proxyAddress)
  );
}
