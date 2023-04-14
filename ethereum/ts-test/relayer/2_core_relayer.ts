import { expect } from "chai"
import { ethers, providers } from "ethers"
import { ChainId, tryNativeToHexString } from "@certusone/wormhole-sdk"
import { ChainInfo, RELAYER_DEPLOYER_PRIVATE_KEY } from "./helpers/consts"
import { generateRandomString } from "./helpers/utils"
import {
  init,
  loadChains,
  loadCoreRelayers,
  loadMockIntegrations,
} from "../../ts-scripts/relayer/helpers/env"
import {relayer, ethers_contracts} from "../../../sdk/js/src"

const ETHEREUM_ROOT = `${__dirname}/..`

init()
const chains = loadChains()
const coreRelayers = loadCoreRelayers()
const mockIntegrations = loadMockIntegrations()

const getWormholeSequenceNumber = (rx: ethers.providers.TransactionReceipt, wormholeAddress: string) => {
  return Number(rx.logs.find((logentry: ethers.providers.Log)=>(logentry.address == wormholeAddress))?.data?.substring(0, 16) || 0);
}

describe("Core Relayer Integration Test - Two Chains", () => {
  // signers

  const sourceChain = chains.find((c) => c.chainId == 2) as ChainInfo
  const targetChain = chains.find((c) => c.chainId == 4) as ChainInfo

  const providerSource = new ethers.providers.StaticJsonRpcProvider(sourceChain.rpc)
  const providerTarget = new ethers.providers.StaticJsonRpcProvider(targetChain.rpc)

  const walletSource = new ethers.Wallet(RELAYER_DEPLOYER_PRIVATE_KEY, providerSource)
  const walletTarget = new ethers.Wallet(RELAYER_DEPLOYER_PRIVATE_KEY, providerTarget)

  const sourceCoreRelayerAddress = coreRelayers.find(
    (p) => p.chainId == sourceChain.chainId
  )?.address as string
  const sourceMockIntegrationAddress = mockIntegrations.find(
    (p) => p.chainId == sourceChain.chainId
  )?.address as string
  const targetCoreRelayerAddress = coreRelayers.find(
    (p) => p.chainId == targetChain.chainId
  )?.address as string
  const targetMockIntegrationAddress = mockIntegrations.find(
    (p) => p.chainId == targetChain.chainId
  )?.address as string

  const sourceCoreRelayer = ethers_contracts.CoreRelayer__factory.connect(
    sourceCoreRelayerAddress,
    walletSource
  )
  const sourceMockIntegration = ethers_contracts.MockRelayerIntegration__factory.connect(
    sourceMockIntegrationAddress,
    walletSource
  )
  const targetCoreRelayer = ethers_contracts.CoreRelayer__factory.connect(
    targetCoreRelayerAddress,
    walletTarget
  )
  const targetMockIntegration = ethers_contracts.MockRelayerIntegration__factory.connect(
    targetMockIntegrationAddress,
    walletTarget
  )

  it("Executes a delivery", async () => {
    const arbitraryPayload = ethers.utils.hexlify(
      ethers.utils.toUtf8Bytes(generateRandomString(32))
    )
    console.log(`Sent message: ${arbitraryPayload}`)
    const value = await sourceCoreRelayer.quoteGas(
      targetChain.chainId,
      500000,
      await sourceCoreRelayer.getDefaultRelayProvider()
    )
    console.log(`Quoted gas delivery fee: ${value}`)
    const tx = await sourceMockIntegration.sendMessage(
      arbitraryPayload,
      targetChain.chainId,
      targetMockIntegrationAddress,
      { value, gasLimit: 500000 }
    )
    console.log("Sent delivery request!")
    const rx = await tx.wait()
    console.log("Message confirmed!")

    await new Promise((resolve) => {
      setTimeout(() => {
        resolve(0)
      }, 8000)
    })

    console.log("Checking if message was relayed")
    const message = await targetMockIntegration.getMessage()
    console.log(`Sent message: ${arbitraryPayload}`)
    console.log(`Received message: ${message}`)
    expect(message).to.equal(arbitraryPayload)
  })

  it("Executes a forward", async () => {
    const arbitraryPayload1 = ethers.utils.hexlify(
      ethers.utils.toUtf8Bytes(generateRandomString(32))
    )
    const arbitraryPayload2 = ethers.utils.hexlify(
      ethers.utils.toUtf8Bytes(generateRandomString(32))
    )
    console.log(`Sent message: ${arbitraryPayload1}`)
    const value = await sourceCoreRelayer.quoteGas(
      targetChain.chainId,
      500000,
      await sourceCoreRelayer.getDefaultRelayProvider()
    )
    const extraForwardingValue = await targetCoreRelayer.quoteGas(
      sourceChain.chainId,
      800000,
      await targetCoreRelayer.getDefaultRelayProvider()
    )
    console.log(`Quoted gas delivery fee: ${value.add(extraForwardingValue)}`)

    const furtherInstructions: MockRelayerIntegration.FurtherInstructionsStruct = {
      keepSending: true,
      newMessages: [arbitraryPayload2, "0x00"],
      chains: [sourceChain.chainId],
      gasLimits: [500000],
    }
    const tx = await sourceMockIntegration.sendMessagesWithFurtherInstructions(
      [arbitraryPayload1],
      furtherInstructions,
      [targetChain.chainId],
      [value.add(extraForwardingValue)],
      { value: value.add(extraForwardingValue), gasLimit: 500000 }
    )
    console.log("Sent delivery request!")
    const rx = await tx.wait()
    console.log("Message confirmed!")

    await new Promise((resolve) => {
      setTimeout(() => {
        resolve(0)
      }, 16000)
    })

    console.log("Checking if message was relayed")
    const message1 = await targetMockIntegration.getMessage()
    console.log(
      `Sent message: ${arbitraryPayload1} (expecting ${arbitraryPayload2} from forward)`
    )
    console.log(`Received message on target: ${message1}`)
    expect(message1).to.equal(arbitraryPayload1)

    console.log("Checking if forward message was relayed back")
    const message2 = await sourceMockIntegration.getMessage()
    console.log(`Sent message: ${arbitraryPayload2}`)
    console.log(`Received message on source: ${message2}`)
    expect(message2).to.equal(arbitraryPayload2)
  })

  it("Executes a multidelivery", async () => {
    const arbitraryPayload1 = ethers.utils.hexlify(
      ethers.utils.toUtf8Bytes(generateRandomString(32))
    )
    console.log(`Sent message: ${arbitraryPayload1}`)
    const value1 = await sourceCoreRelayer.quoteGas(
      sourceChain.chainId,
      500000,
      await sourceCoreRelayer.getDefaultRelayProvider()
    )
    const value2 = await sourceCoreRelayer.quoteGas(
      targetChain.chainId,
      500000,
      await sourceCoreRelayer.getDefaultRelayProvider()
    )
    console.log(`Quoted gas delivery fee: ${value1.add(value2)}`)

    const furtherInstructions: MockRelayerIntegration.FurtherInstructionsStruct = {
      keepSending: false,
      newMessages: [],
      chains: [],
      gasLimits: [],
    }
    const tx = await sourceMockIntegration.sendMessagesWithFurtherInstructions(
      [arbitraryPayload1],
      furtherInstructions,
      [sourceChain.chainId, targetChain.chainId],
      [value1, value2],
      { value: value1.add(value2), gasLimit: 500000 }
    )
    console.log("Sent delivery request!")
    const rx = await tx.wait()
    console.log("Message confirmed!")

    await new Promise((resolve) => {
      setTimeout(() => {
        resolve(0)
      }, 8000)
    })

    console.log("Checking if first message was relayed")
    const message1 = await sourceMockIntegration.getMessage()
    console.log(
      `Sent message: ${arbitraryPayload1}`
    )
    console.log(`Received message: ${message1}`)
    expect(message1).to.equal(arbitraryPayload1)

    console.log("Checking if second message was relayed")
    const message2 = await targetMockIntegration.getMessage()
    console.log(`Sent message: ${arbitraryPayload1}`)
    console.log(`Received message: ${message2}`)
    expect(message2).to.equal(arbitraryPayload1)
  })
  it("Executes a multiforward", async () => {
    const arbitraryPayload1 = ethers.utils.hexlify(
      ethers.utils.toUtf8Bytes(generateRandomString(32))
    )
    const arbitraryPayload2 = ethers.utils.hexlify(
      ethers.utils.toUtf8Bytes(generateRandomString(32))
    )
    console.log(`Sent message: ${arbitraryPayload1}`)
    const value1 = await sourceCoreRelayer.quoteGas(
      targetChain.chainId,
      1000000,
      await sourceCoreRelayer.getDefaultRelayProvider()
    )
    const value2 = (await targetCoreRelayer.quoteGas(
      sourceChain.chainId,
      1000000,
      await targetCoreRelayer.getDefaultRelayProvider()
    ))
    const value3 = (await targetCoreRelayer.quoteGas(
      targetChain.chainId,
      1000000,
      await targetCoreRelayer.getDefaultRelayProvider()
    ))
    console.log(`Quoted gas delivery fee: ${value1.add(value2).add(value3)}`)

    const furtherInstructions: MockRelayerIntegration.FurtherInstructionsStruct = {
      keepSending: true,
      newMessages: [arbitraryPayload2, "0x00"],
      chains: [sourceChain.chainId, targetChain.chainId],
      gasLimits: [1000000, 1000000],
    }
    const tx = await sourceMockIntegration.sendMessagesWithFurtherInstructions(
      [arbitraryPayload1],
      furtherInstructions,
      [targetChain.chainId],
      [value1.add(value2).add(value3)],
      { value: value1.add(value2).add(value3), gasLimit: 500000 }
    )
    console.log("Sent delivery request!")
    const rx = await tx.wait()
    console.log("Message confirmed!")

    await new Promise((resolve) => {
      setTimeout(() => {
        resolve(0)
      }, 16000)
    })

    console.log("Checking if first forward was relayed")
    const message1 = await sourceMockIntegration.getMessage()
    console.log(
      `Sent message: ${arbitraryPayload2}`
    )
    console.log(`Received message: ${message1}`)
    expect(message1).to.equal(arbitraryPayload2)

    console.log("Checking if second forward was relayed")
    const message2 = await targetMockIntegration.getMessage()
    console.log(
      `Sent message: ${arbitraryPayload2}`
    )
    console.log(`Received message: ${message2}`)
    expect(message2).to.equal(arbitraryPayload2)
  })

  it("Executes a delivery that results in a forward failure", async () => {
    const arbitraryPayload1 = ethers.utils.hexlify(
      ethers.utils.toUtf8Bytes(generateRandomString(32))
    )
    const arbitraryPayload2 = ethers.utils.hexlify(
      ethers.utils.toUtf8Bytes(generateRandomString(32))
    )
    console.log(`Sent message: ${arbitraryPayload1}`)
    const value = await sourceCoreRelayer.quoteGas(
      targetChain.chainId,
      500000,
      await sourceCoreRelayer.getDefaultRelayProvider()
    )
    const notEnoughExtraForwardingValue = await targetCoreRelayer.quoteGas(
      sourceChain.chainId,
      10000,
      await targetCoreRelayer.getDefaultRelayProvider()
    )
    const enoughExtraForwardingValue = await targetCoreRelayer.quoteGas(
      sourceChain.chainId,
      500000,
      await targetCoreRelayer.getDefaultRelayProvider()
    )
    console.log(`Quoted gas delivery fee: ${value.add(notEnoughExtraForwardingValue)}`)

    const furtherInstructions: MockRelayerIntegration.FurtherInstructionsStruct = {
      keepSending: true,
      newMessages: [arbitraryPayload2, "0x00"],
      chains: [sourceChain.chainId],
      gasLimits: [500000],
    }
    const tx = await sourceMockIntegration.sendMessagesWithFurtherInstructions(
      [arbitraryPayload1],
      furtherInstructions,
      [targetChain.chainId],
      [value.add(notEnoughExtraForwardingValue)],
      { value: value.add(notEnoughExtraForwardingValue), gasLimit: 500000 })
      
      console.log("Sent delivery request!")
      const rx = await tx.wait()
      console.log("Message confirmed!")

      await new Promise((resolve) => {
        setTimeout(() => {
          resolve(0)
        }, 4000)
      })
  
      console.log("Checking if message was relayed (it shouldn't have been!")
      const message1 = await targetMockIntegration.getMessage()
      console.log(
        `Sent message: ${arbitraryPayload1} (expecting ${arbitraryPayload2} from forward)`
      )
      console.log(`Received message on target: ${message1}`)
      expect(message1).to.not.equal(arbitraryPayload1)
  
      console.log("Checking if forward message was relayed back (it shouldn't have been!)")
      const message2 = await sourceMockIntegration.getMessage()
      console.log(`Sent message: ${arbitraryPayload2}`)
      console.log(`Received message on source: ${message2}`)
      expect(message2).to.not.equal(arbitraryPayload2)

      let info: DeliveryInfo = (await relayer.getWormholeRelayerInfo({environment: "DEVNET", sourceChain: sourceChain.chainId, sourceTransaction: tx.hash})) as DeliveryInfo
      let status = info.targetChainStatus.events[0].status
      expect(status).to.equal("Forward Request Failure")
    })


    
  it("Tests the Typescript SDK with a Delivery Success", async () => {
    const arbitraryPayload = ethers.utils.hexlify(
      ethers.utils.toUtf8Bytes(generateRandomString(32))
    )
    console.log(`Sent message: ${arbitraryPayload}`)
    const value = await sourceCoreRelayer.quoteGas(
      targetChain.chainId,
      500000,
      await sourceCoreRelayer.getDefaultRelayProvider()
    )
    console.log(`Quoted gas delivery fee: ${value}`)
    const tx = await sourceMockIntegration.sendMessage(
      arbitraryPayload,
      targetChain.chainId,
      targetMockIntegrationAddress,
      { value, gasLimit: 500000 }
    )

    console.log("Sent delivery request!")
    const rx = await tx.wait()
    console.log("Message confirmed!")

    console.log("Checking status using SDK");
    let info: DeliveryInfo = (await relayer.getWormholeRelayerInfo({environment: "DEVNET", sourceChain: sourceChain.chainId, sourceTransaction: tx.hash})) as DeliveryInfo
    let status = info.targetChainStatus.events[0].status

    expect(status.substring(0, 22)).to.equal("Delivery didn't happen")

    await new Promise((resolve) => {
      setTimeout(() => {
        resolve(0)
      }, 6000)
    })
    
    const message = await targetMockIntegration.getMessage()
    console.log(`Sent message: ${arbitraryPayload}`)
    console.log(`Received message: ${message}`)
    expect(message).to.equal(arbitraryPayload)

    console.log("Checking status using SDK");
    info = await relayer.getWormholeRelayerInfo({environment: "DEVNET", sourceChain: sourceChain.chainId, sourceTransaction: tx.hash}) as DeliveryInfo;
    status = info.targetChainStatus.events[0].status
    expect(status).to.equal("Delivery Success")
  })

    
  it("Tests the Typescript SDK with a Delivery Failure", async () => {
    const arbitraryPayload = ethers.utils.hexlify(
      ethers.utils.toUtf8Bytes(generateRandomString(32))
    )
    console.log(`Sent message: ${arbitraryPayload}`)
    const valueNotEnough = await sourceCoreRelayer.quoteGas(
      targetChain.chainId,
      10000,
      await sourceCoreRelayer.getDefaultRelayProvider()
    )
    const value = await sourceCoreRelayer.quoteGas(
      targetChain.chainId,
      500000,
      await sourceCoreRelayer.getDefaultRelayProvider()
    )
    console.log(`Quoted gas delivery fee: ${value}`)
    const tx = await sourceMockIntegration.sendMessage(
      arbitraryPayload,
      targetChain.chainId,
      targetMockIntegrationAddress,
      { value: valueNotEnough, gasLimit: 500000 }
    )
    console.log("Sent delivery request!")
    const rx = await tx.wait()
    console.log("Message confirmed!")

    console.log("Checking status using SDK");
    let info: DeliveryInfo = (await relayer.getWormholeRelayerInfo({environment: "DEVNET", sourceChain: sourceChain.chainId, sourceTransaction: tx.hash })) as DeliveryInfo
    let status = info.targetChainStatus.events[0].status
    expect(status.substring(0, 22)).to.equal("Delivery didn't happen")

    await new Promise((resolve) => {
      setTimeout(() => {
        resolve(0)
      }, 6000)
    })

    const message = await targetMockIntegration.getMessage()
    console.log(`Sent message: ${arbitraryPayload}`)
    console.log(`Received message: ${message}`)
    expect(message).to.not.equal(arbitraryPayload)

    console.log("Checking status using SDK");
    info = await relayer.getWormholeRelayerInfo({environment: "DEVNET", sourceChain: sourceChain.chainId, sourceTransaction: tx.hash}) as DeliveryInfo;
    status = info.targetChainStatus.events[0].status
    expect(status).to.equal("Receiver Failure")
  })
  
})
    
