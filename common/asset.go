package common

import (
	"fmt"
	"strings"

	"github.com/MixinNetwork/mixin/crypto"
)

var (
	XINAssetId          crypto.Hash
	BitcoinAssetId      crypto.Hash
	EthereumAssetId     crypto.Hash
	BOXAssetId          crypto.Hash
	MOBAssetId          crypto.Hash
	USDTEthereumAssetId crypto.Hash
	USDTTronAssetId     crypto.Hash
	PandoUSDAssetId     crypto.Hash
	USDCAssetId         crypto.Hash
	EOSAssetId          crypto.Hash

	XINAsset *Asset
)

type Asset struct {
	Chain    crypto.Hash
	AssetKey string
}

func init() {
	XINAssetId = crypto.Sha256Hash([]byte("c94ac88f-4671-3976-b60a-09064f1811e8"))
	BitcoinAssetId = crypto.Sha256Hash([]byte("c6d0c728-2624-429b-8e0d-d9d19b6592fa"))
	EthereumAssetId = crypto.Sha256Hash([]byte("43d61dcd-e413-450d-80b8-101d5e903357"))
	BOXAssetId = crypto.Sha256Hash([]byte("f5ef6b5d-cc5a-3d90-b2c0-a2fd386e7a3c"))
	MOBAssetId = crypto.Sha256Hash([]byte("eea900a8-b327-488c-8d8d-1428702fe240"))
	USDTEthereumAssetId = crypto.Sha256Hash([]byte("4d8c508b-91c5-375b-92b0-ee702ed2dac5"))
	USDTTronAssetId = crypto.Sha256Hash([]byte("b91e18ff-a9ae-3dc7-8679-e935d9a4b34b"))
	PandoUSDAssetId = crypto.Sha256Hash([]byte("31d2ea9c-95eb-3355-b65b-ba096853bc18"))
	USDCAssetId = crypto.Sha256Hash([]byte("9b180ab6-6abe-3dc0-a13f-04169eb34bfa"))
	EOSAssetId = crypto.Sha256Hash([]byte("6cfe566e-4aad-470b-8c9a-2fd35b49c68d"))

	XINAsset = &Asset{Chain: EthereumAssetId, AssetKey: "0xa974c709cfb4566686553a20790685a47aceaa33"}
}

func (a *Asset) Verify() error {
	if !a.Chain.HasValue() {
		return fmt.Errorf("invalid asset chain %v", *a)
	}
	if strings.TrimSpace(a.AssetKey) != a.AssetKey || len(a.AssetKey) == 0 {
		return fmt.Errorf("invalid asset key %v", *a)
	}
	return nil
}

func GetAssetCapacity(id crypto.Hash) Integer {
	switch id {
	case BitcoinAssetId:
		return NewIntegerFromString("10000")
	case EthereumAssetId:
		return NewIntegerFromString("30000")
	case XINAssetId:
		return NewIntegerFromString("1000000")
	case BOXAssetId:
		return NewIntegerFromString("200000000")
	case MOBAssetId:
		return NewIntegerFromString("50000000")
	case USDTEthereumAssetId:
		return NewIntegerFromString("5000000")
	case USDTTronAssetId:
		return NewIntegerFromString("10000000")
	case PandoUSDAssetId:
		return NewIntegerFromString("1000000000000")
	case USDCAssetId:
		return NewIntegerFromString("5000000")
	case EOSAssetId:
		return NewIntegerFromString("10000000")
	default: // TODO more assets and better default value
		return NewIntegerFromString("115792089237316195423570985008687907853269984665640564039457.58400791")
	}
}
