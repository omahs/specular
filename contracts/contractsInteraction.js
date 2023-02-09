const ethers = require('ethers');
const inboxV2 = require('./artifacts/src/SequencerInboxV2.sol/SequencerInboxV2.json');

const main = async() => {

  const contractAddress = "0xC1ea681dC46623DB78c06d0dE034ccac3279FF76"
  const provider = new ethers.providers.JsonRpcProvider("https://rpc.chiadochain.net");
  const code = await provider.getCode(contractAddress)
  const signer = new ethers.Wallet("ff253124edfef0057bbb8311bafb3bcffb87c0be32649ae9c396ccc98825d5bc",provider);
  const contract = new ethers.Contract(contractAddress, inboxV2.abi, provider);
  
  let inboxSize = await contract.getInboxSize();
  console.log("InboxSize: ",Number(inboxSize))

  const sequencerAddress = await contract.sequencerAddress();
  console.log("Sequencer Address",sequencerAddress)

  let getStateVariable = await contract.getUpgradeRelatedStateVariable();
  console.log("StateVariable: ",Number(getStateVariable))

  const setStateVariable = await contract.connect(signer).setUpgradeRelatedStateVariable(7845);
  await setStateVariable.wait();
  console.log("Setting state variable to 7845 ...")

  getStateVariable = await contract.getUpgradeRelatedStateVariable();
  console.log("StateVariable: ",Number(getStateVariable))

}


main();