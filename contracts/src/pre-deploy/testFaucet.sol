// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.0;

import "@openzeppelin/contracts/access/Ownable.sol";

contract TinyFaucet is Ownable {
    uint256 public amountAllowed = 1e16;

    event LogDepositReceived(address, uint256);
    event LogRequestFunds(address, uint256);

    mapping(address => uint256) public lockTime;

    constructor() payable {}

    receive() external payable {
        emit LogDepositReceived(msg.sender, msg.value);
    }

    function retrieve() external onlyOwner {
        payable(msg.sender).transfer(address(this).balance);
    }

    function requestFunds(address payable _requestor) public payable onlyOwner {
        require(block.timestamp > lockTime[_requestor], "Lock time has not expired.");
        require(address(this).balance > amountAllowed, "Not enough funds in faucet.");

        _requestor.transfer(amountAllowed);

        lockTime[_requestor] = block.timestamp + 1 days;
        emit LogRequestFunds(_requestor, amountAllowed);
    }
}
