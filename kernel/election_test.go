package kernel

import (
	"os"
	"testing"
	"time"

	"github.com/MixinNetwork/mixin/common"
	"github.com/MixinNetwork/mixin/config"
	"github.com/MixinNetwork/mixin/crypto"
	"github.com/MixinNetwork/mixin/storage"
	"github.com/dgraph-io/ristretto"
	"github.com/stretchr/testify/require"
)

const mainnetId = "74c6cdb7d51af57037faa1f5544f8331ced001df5964331911ca51385993b375"

func TestNodeRemovePossibility(t *testing.T) {
	require := require.New(t)

	root, err := os.MkdirTemp("", "mixin-election-test")
	require.Nil(err)
	defer os.RemoveAll(root)

	node := setupTestNode(require, root)
	require.NotNil(node)

	now, err := time.Parse(time.RFC3339, "2020-02-09T00:00:00Z")
	require.Nil(err)
	candi, err := node.checkRemovePossibility(node.IdForNetwork, uint64(now.UnixNano()), nil)
	require.Nil(candi)
	require.NotNil(err)
	require.Contains(err.Error(), "invalid node remove hour")

	now, err = time.Parse(time.RFC3339, "2021-03-10T17:00:00Z")
	require.Nil(err)
	candi, err = node.checkRemovePossibility(node.IdForNetwork, uint64(now.UnixNano()), nil)
	require.Nil(err)
	require.NotNil(candi)
	require.Equal("009234939f0f8f9495f611c713ec61358262ecf6ec742671addfcce5350c1d23", candi.IdForNetwork.String())

	tx, err := node.buildNodeRemoveTransaction(node.IdForNetwork, uint64(now.UnixNano()), nil)
	require.Nil(err)
	require.NotNil(tx)
	require.Equal("c183ce86b5de6c35395371eebf9dbe7a27f06fa3bc5f8aae16a8e833bced422b", tx.PayloadHash().String())
	require.Equal(uint8(5), tx.Version)
	require.Equal(common.XINAssetId, tx.Asset)
	require.Equal(KernelNodePledgeAmount, tx.Outputs[0].Amount)
	require.Equal("fffe01", tx.Outputs[0].Script.String())
	require.Equal(uint8(common.OutputTypeNodeRemove), tx.Outputs[0].Type)
	require.Equal(uint8(common.TransactionTypeNodeRemove), tx.TransactionType())
	require.Len(tx.Outputs[0].Keys, 1)

	err = tx.SignInput(node.persistStore, 0, []*common.Address{&node.Signer})
	require.NotNil(err)
	require.Contains(err.Error(), "invalid key for the input")
	err = tx.Validate(node.persistStore, uint64(time.Now().UnixNano()), false)
	require.Nil(err)

	payee, err := common.NewAddressFromString("XIN4GLKJRtaquYDE49MraHWeKKyoWVmS58qvXQY845pxLECzm86RmkVZEwWMHo8ZRMd2Q8MziDvre5RrC8Lkty4kFeuZ2aYg")
	require.Nil(err)
	mask := tx.Outputs[0].Mask
	ghost := tx.Outputs[0].Keys[0]
	view := payee.PublicSpendKey.DeterministicHashDerive()
	require.Equal(payee.PublicSpendKey.String(), crypto.ViewGhostOutputKey(ghost, &view, &mask, 0).String())
}

var configData = []byte(`[node]
signer-key = "56a7904a2dfd71c397bb48584033d8cb6ddcde9b46b7d91f07d2ede061723a0b"
consensus-only = true
memory-cache-size = 16
cache-ttl = 7200
ring-cache-size = 4096
ring-final-size = 16384
[network]
listener = "mixin-node.example.com:7239"`)

func setupTestNode(require *require.Assertions, dir string) *Node {
	err := os.WriteFile(dir+"/config.toml", configData, 0644)
	require.Nil(err)

	data, err := os.ReadFile("../config/genesis.json")
	require.Nil(err)
	err = os.WriteFile(dir+"/genesis.json", data, 0644)
	require.Nil(err)

	custom, err := config.Initialize(dir + "/config.toml")
	require.Nil(err)

	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 1e7, // number of keys to track frequency of (10M).
		MaxCost:     1 << 30,
		BufferItems: 64, // number of keys per Get buffer.
	})
	require.Nil(err)

	store, err := storage.NewBadgerStore(custom, dir)
	require.Nil(err)
	require.NotNil(store)
	node, err := SetupNode(custom, store, cache, ":7239", dir)
	require.Nil(err)
	require.Equal(mainnetId, node.networkId.String())
	return node
}
