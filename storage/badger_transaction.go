package storage

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/MixinNetwork/mixin/common"
	"github.com/MixinNetwork/mixin/config"
	"github.com/MixinNetwork/mixin/crypto"
	"github.com/dgraph-io/badger/v4"
)

func (s *BadgerStore) ReadTransaction(hash crypto.Hash) (*common.VersionedTransaction, string, error) {
	txn := s.snapshotsDB.NewTransaction(false)
	defer txn.Discard()

	return readTransactionAndFinalization(txn, hash)
}

func readTransactionAndFinalization(txn *badger.Txn, hash crypto.Hash) (*common.VersionedTransaction, string, error) {
	tx, err := readTransaction(txn, hash)
	if err != nil || tx == nil {
		return tx, "", err
	}
	key := graphFinalizationKey(hash)
	item, err := txn.Get(key)
	if err == badger.ErrKeyNotFound {
		return tx, "", nil
	} else if err != nil {
		return tx, "", err
	}
	val, err := item.ValueCopy(nil)
	if err != nil {
		return tx, "", err
	}
	var final crypto.Hash
	if len(val) != len(final) {
		panic(len(val))
	}
	copy(final[:], val)
	return tx, final.String(), nil
}

func (s *BadgerStore) WriteTransaction(ver *common.VersionedTransaction) error {
	txn := s.snapshotsDB.NewTransaction(true)
	defer txn.Discard()

	// FIXME assert kind checks, not needed at all
	if config.Debug {
		txHash := ver.PayloadHash()
		for _, in := range ver.Inputs {
			if len(in.Genesis) > 0 {
				continue
			}

			if in.Deposit != nil {
				ival, err := readDepositInput(txn, in.Deposit)
				if err != nil {
					panic(fmt.Errorf("deposit check error %s", err.Error()))
				}
				if !bytes.Equal(ival, txHash[:]) {
					panic(fmt.Errorf("deposit locked for transaction %s", hex.EncodeToString(ival)))
				}
				continue
			}

			if in.Mint != nil {
				dist, err := readMintInput(txn, in.Mint)
				if err != nil {
					panic(fmt.Errorf("mint check error %s", err.Error()))
				}
				if dist.Transaction != txHash || dist.Amount.Cmp(in.Mint.Amount) != 0 {
					panic(fmt.Errorf("mint locked for transaction %s", dist.Transaction.String()))
				}
				continue
			}

			key := graphUtxoKey(in.Hash, in.Index)
			item, err := txn.Get(key)
			if err != nil {
				panic(fmt.Errorf("UTXO check error %s %s:%d=>%s", err.Error(), in.Hash.String(), in.Index, txHash.String()))
			}
			ival, err := item.ValueCopy(nil)
			if err != nil {
				panic(fmt.Errorf("UTXO check error %s", err.Error()))
			}
			out, err := common.UnmarshalUTXO(ival)
			if err != nil {
				panic(fmt.Errorf("UTXO check error %s", err.Error()))
			}
			if out.LockHash != txHash {
				panic(fmt.Errorf("utxo %s:%d locked for transaction %s instead of %s", out.Hash, out.Index, out.LockHash, txHash))
			}
		}
	}
	// assert end

	err := writeTransaction(txn, ver)
	if err != nil {
		return err
	}
	return txn.Commit()
}

func readTransaction(txn *badger.Txn, hash crypto.Hash) (*common.VersionedTransaction, error) {
	key := graphTransactionKey(hash)
	item, err := txn.Get(key)
	if err == badger.ErrKeyNotFound {
		return nil, nil
	}
	val, err := item.ValueCopy(nil)
	if err != nil {
		return nil, err
	}
	return common.UnmarshalVersionedTransaction(val)
}

func pruneTransaction(txn *badger.Txn, hash crypto.Hash) error {
	key := graphFinalizationKey(hash)
	_, err := txn.Get(key)
	if err == nil {
		return fmt.Errorf("prune finalized transaction %s", hash.String())
	} else if err != badger.ErrKeyNotFound {
		return err
	}
	key = graphTransactionKey(hash)
	return txn.Delete(key)
}

func writeTransaction(txn *badger.Txn, ver *common.VersionedTransaction) error {
	key := graphTransactionKey(ver.PayloadHash())

	_, err := txn.Get(key)
	if err == nil {
		return nil
	} else if err != badger.ErrKeyNotFound {
		return err
	}

	val := ver.Marshal()
	return txn.Set(key, val)
}

func finalizeTransaction(txn *badger.Txn, ver *common.VersionedTransaction, snap *common.SnapshotWithTopologicalOrder) error {
	key := graphFinalizationKey(ver.PayloadHash())
	_, err := txn.Get(key)
	if err == nil {
		return nil
	} else if err != badger.ErrKeyNotFound {
		return err
	}
	snapHash := snap.PayloadHash()
	err = txn.Set(key, snapHash[:])
	if err != nil {
		return err
	}

	if d := ver.Inputs[0].Deposit; d != nil {
		err := writeAssetInfo(txn, ver.Asset, d.Asset())
		if err != nil {
			return err
		}
	}

	genesis := len(ver.Inputs[0].Genesis) > 0
	for _, utxo := range ver.UnspentOutputs() {
		err := writeUTXO(txn, utxo, ver.Extra, snap.Timestamp, genesis)
		if err != nil {
			return err
		}
	}

	return writeTotalInAsset(txn, ver)
}

func writeUTXO(txn *badger.Txn, utxo *common.UTXOWithLock, extra []byte, timestamp uint64, genesis bool) error {
	for _, k := range utxo.Keys {
		err := lockGhostKey(txn, k, utxo.Hash, true)
		if err != nil {
			return err
		}
	}
	key := graphUtxoKey(utxo.Hash, utxo.Index)
	val := utxo.Marshal()
	err := txn.Set(key, val)
	if err != nil {
		return err
	}

	var signer, payee crypto.Key
	if len(extra) >= len(signer) {
		copy(signer[:], extra)
		copy(payee[:], extra[len(signer):])
	}
	switch utxo.Type {
	case common.OutputTypeNodePledge:
		return writeNodePledge(txn, signer, payee, utxo.Hash, timestamp)
	case common.OutputTypeNodeCancel:
		return writeNodeCancel(txn, signer, payee, utxo.Hash, timestamp)
	case common.OutputTypeNodeAccept:
		return writeNodeAccept(txn, signer, payee, utxo.Hash, timestamp, genesis)
	case common.OutputTypeNodeRemove:
		return writeNodeRemove(txn, signer, payee, utxo.Hash, timestamp)
	case common.OutputTypeCustodianUpdateNodes:
		return writeCustodianNodes(txn, timestamp, utxo, extra, genesis)
	}

	return nil
}

func graphTransactionKey(hash crypto.Hash) []byte {
	return append([]byte(graphPrefixTransaction), hash[:]...)
}

func graphFinalizationKey(hash crypto.Hash) []byte {
	return append([]byte(graphPrefixFinalization), hash[:]...)
}

func graphUniqueKey(nodeId, hash crypto.Hash) []byte {
	key := append(hash[:], nodeId[:]...)
	return append([]byte(graphPrefixUnique), key...)
}

func graphGhostKey(k crypto.Key) []byte {
	return append([]byte(graphPrefixGhost), k[:]...)
}

func graphUtxoKey(hash crypto.Hash, index uint) []byte {
	if index > 1024 {
		panic(index)
	}
	buf := make([]byte, binary.MaxVarintLen64)
	size := binary.PutVarint(buf, int64(index))
	key := append([]byte(graphPrefixUTXO), hash[:]...)
	return append(key, buf[:size]...)
}
