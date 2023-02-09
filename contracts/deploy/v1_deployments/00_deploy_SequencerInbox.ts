import {HardhatRuntimeEnvironment} from 'hardhat/types';
import {DeployFunction} from 'hardhat-deploy/types';

const deploySequencer: DeployFunction = async function (hre: HardhatRuntimeEnvironment) {
    const {deployments, getNamedAccounts, ethers, upgrades} = hre;
    const {deploy} = deployments;
    const {sequencer} = await getNamedAccounts();
    
    console.log(sequencer);

    const Inbox = await ethers.getContractFactory("SequencerInbox");
    const inbox = await upgrades.deployProxy(Inbox, [sequencer], {initializer: 'initialize', from : sequencer, timeout: 0});
    
    await inbox.deployed();
    console.log("inbox Proxy:", inbox.address);
    console.log("inbox Implementation Address", await upgrades.erc1967.getImplementationAddress(inbox.address));
    console.log("inbox Admin Address", await upgrades.erc1967.getAdminAddress(inbox.address))    
  
}

deploySequencer.tags = ['SequencerInbox', 'V1'];
export default deploySequencer;;