import {HardhatRuntimeEnvironment} from 'hardhat/types';
import {DeployFunction} from 'hardhat-deploy/types';
import {Manifest} from '@openzeppelin/upgrades-core';

const deploySequencer: DeployFunction = async function (hre: HardhatRuntimeEnvironment) {
    const {deployments, getNamedAccounts, ethers, upgrades, network} = hre;
    const {deploy} = deployments;
    const {sequencer} = await getNamedAccounts();
    const {provider} = await network;
    const manifest = await Manifest.forNetwork(provider);

    const proxies = (await manifest.read()).proxies;

    const sequencerInboxProxyAddress = proxies[0].address;

    const InboxV2 = await ethers.getContractFactory('SequencerInboxV2');

    const inboxV2 = await upgrades.upgradeProxy(sequencerInboxProxyAddress, InboxV2);
    
    await inboxV2.deployed();
    console.log("inbox Proxy:", inboxV2.address);
    console.log("inbox Implementation Address", await upgrades.erc1967.getImplementationAddress(inboxV2.address));
    console.log("inbox Admin Address", await upgrades.erc1967.getAdminAddress(inboxV2.address))    
  
}

deploySequencer.tags = ['SequencerInbox', 'V2'];
export default deploySequencer;;