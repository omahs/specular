import "dotenv/config";
import "@nomiclabs/hardhat-ethers";
import { ethers } from "ethers";
import { HardhatUserConfig } from "hardhat/types";
import "@nomiclabs/hardhat-etherscan";
import "@nomiclabs/hardhat-waffle";
import "hardhat-abi-exporter";
import "hardhat-deploy";
import "@openzeppelin/hardhat-upgrades";
import "@typechain/hardhat";

const mnemonic =
  process.env.MNEMONIC ??
  "test test test test test test test test test test test junk";

const wallet = ethers.Wallet.fromMnemonic(mnemonic);
const INFURA_KEY = process.env.INFURA_KEY || "";
const ALCHEMY_KEY = process.env.ALCHEMY_KEY || "";
const ETHERSCAN_API_KEY = process.env.ETHERSCAN_API_KEY || "";
const SEQUENCER_PRIVATE_KEY = process.env.SEQUENCER_PRIVATE_KEY;
const SEQUENCER_ADDRESS = process.env.SEQUENCER_ADDRESS ?? wallet.address;
const DEPLOYER_PRIVATE_KEY = process.env.DEPLOYER_PRIVATE_KEY;
const DEPLOYER_ADDRESS = process.env.DEPLOYER_ADDRESS ?? wallet.address;

function createConfig(network: string) {
  const config = {
    url: getNetworkURL(network),
    accounts: getNetworkAccounts(network),
    saveDeployments: true,
  };
  if (network.startsWith("specular")) {
    config.deploy = ["deploy-l2/"];
  }
  if (network === "specularLocalDev") {
    config.companionNetworks = { l1: "localhost" };
  } else if (network === "localhost") {
    config.companionNetworks = { l2: "specularLocalDev" };
  } else if (network === "specularDev") {
    config.companionNetworks = { l1: "chiado" };
  } else if (network === "chiado") {
    config.companionNetworks = { l2: "specularDev" };
  }
  return config;
}

function getNetworkAccounts(network: string) {
  if (
    network === "goerli" ||
    network === "chiado" ||
    network === "specularDev"
  ) {
    return SEQUENCER_PRIVATE_KEY
      ? [`0x${SEQUENCER_PRIVATE_KEY}`, `0x${DEPLOYER_PRIVATE_KEY}`]
      : { mnemonic };
  } else {
    return { mnemonic };
  }
}

function getNetworkURL(network: string) {
  if (network === "mainnet") {
    return `https://mainnet.infura.io/v3/${INFURA_KEY}`;
  } else if (network === "goerli") {
    return `https://goerli.infura.io/v3/${INFURA_KEY}`;
  } else if (network === "sepolia") {
    return `https://sepolia.infura.io/v3/${INFURA_KEY}`;
  } else if (network === "gnosis") {
    return "https://rpc.gnosischain.com/";
  } else if (network === "chiado") {
    return "https://rpc.chiadochain.net";
  } else if (network === "specularLocalDev") {
    return "http://localhost:4011";
  } else if (network === "specularDev") {
    return "https://devnet.specular.network";
  }
}

const config: HardhatUserConfig = {
  defaultNetwork: "hardhat",
  solidity: {
    version: "0.8.9",
    settings: {
      optimizer: {
        enabled: true,
        runs: 200,
      },
    },
  },
  paths: {
    sources: "./src",
  },
  abiExporter: {
    path: "./abi",
    runOnCompile: true,
    clear: true,
  },
  namedAccounts: {
    sequencer: {
      default: SEQUENCER_ADDRESS,
      hardhat: 0,
      localhost: 0,
    },
    validator: 1,
    deployer: {
      default: DEPLOYER_ADDRESS,
      hardhat: 2,
      localhost: 2,
    },
  },
  networks: {
    mainnet: createConfig("mainnet"),
    sepolia: createConfig("sepolia"),
    goerli: createConfig("goerli"),
    gnosis: createConfig("gnosis"),
    chiado: createConfig("chiado"),
    specularLocalDev: createConfig("specularLocalDev"),
    specularDev: createConfig("specularDev"),
    hardhat: {
      gas: "auto",
      mining: {
        auto: true,
        interval: 5000,
        mempool: {
          order: "fifo",
        },
      },
    },
  },
};

export default config;
