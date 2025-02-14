package network

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/MixinNetwork/mixin/common"
	"github.com/MixinNetwork/mixin/crypto"
	"github.com/MixinNetwork/mixin/logger"
	"github.com/dgraph-io/ristretto"
)

const (
	PeerMessageTypePing               = 1
	PeerMessageTypeAuthentication     = 3
	PeerMessageTypeGraph              = 4
	PeerMessageTypeSnapshotConfirm    = 5
	PeerMessageTypeTransactionRequest = 6
	PeerMessageTypeTransaction        = 7

	PeerMessageTypeSnapshotAnnouncement = 10 // leader send snapshot to peer
	PeerMessageTypeSnapshotCommitment   = 11 // peer generate ri based, send Ri to leader
	PeerMessageTypeTransactionChallenge = 12 // leader send bitmask Z and aggregated R to peer
	PeerMessageTypeSnapshotResponse     = 13 // peer generate A from nodes and Z, send response si = ri + H(R || A || M)ai to leader
	PeerMessageTypeSnapshotFinalization = 14 // leader generate A, verify si B = ri B + H(R || A || M)ai B = Ri + H(R || A || M)Ai, then finalize based on threshold
	PeerMessageTypeCommitments          = 15
	PeerMessageTypeFullChallenge        = 16

	PeerMessageTypeBundle          = 100
	PeerMessageTypeGossipNeighbors = 101

	MaxMessageBundleSize = 16
)

type PeerMessage struct {
	Type            uint8
	Snapshot        *common.Snapshot
	SnapshotHash    crypto.Hash
	Transaction     *common.VersionedTransaction
	TransactionHash crypto.Hash
	Cosi            crypto.CosiSignature
	Commitment      crypto.Key
	Challenge       crypto.Key
	Response        [32]byte
	WantTx          bool
	Commitments     []*crypto.Key
	Graph           []*SyncPoint
	Data            []byte
	Neighbors       []string
}

type SyncHandle interface {
	GetCacheStore() *ristretto.Cache
	BuildAuthenticationMessage() []byte
	Authenticate(msg []byte) (crypto.Hash, string, error)
	UpdateNeighbors(neighbors []string) error
	BuildGraph() []*SyncPoint
	UpdateSyncPoint(peerId crypto.Hash, points []*SyncPoint)
	ReadAllNodesWithoutState() []crypto.Hash
	ReadSnapshotsSinceTopology(offset, count uint64) ([]*common.SnapshotWithTopologicalOrder, error)
	ReadSnapshotsForNodeRound(nodeIdWithNetwork crypto.Hash, round uint64) ([]*common.SnapshotWithTopologicalOrder, error)
	SendTransactionToPeer(peerId, tx crypto.Hash) error
	CachePutTransaction(peerId crypto.Hash, ver *common.VersionedTransaction) error
	CosiQueueExternalAnnouncement(peerId crypto.Hash, s *common.Snapshot, R *crypto.Key) error
	CosiAggregateSelfCommitments(peerId crypto.Hash, snap crypto.Hash, commitment *crypto.Key, wantTx bool) error
	CosiQueueExternalChallenge(peerId crypto.Hash, snap crypto.Hash, cosi *crypto.CosiSignature, ver *common.VersionedTransaction) error
	CosiQueueExternalFullChallenge(peerId crypto.Hash, s *common.Snapshot, commitment, challenge *crypto.Key, cosi *crypto.CosiSignature, ver *common.VersionedTransaction) error
	CosiAggregateSelfResponses(peerId crypto.Hash, snap crypto.Hash, response *[32]byte) error
	VerifyAndQueueAppendSnapshotFinalization(peerId crypto.Hash, s *common.Snapshot) error
	CosiQueueExternalCommitments(peerId crypto.Hash, commitments []*crypto.Key) error
}

func (me *Peer) SendCommitmentsMessage(idForNetwork crypto.Hash, commitments []*crypto.Key) error {
	data := buildCommitmentsMessage(commitments)
	hash := crypto.Blake3Hash(data)
	key := append(idForNetwork[:], 'C', 'R')
	key = append(key, hash[:]...)
	return me.sendHighToPeer(idForNetwork, PeerMessageTypeCommitments, key, data)
}

func (me *Peer) SendSnapshotAnnouncementMessage(idForNetwork crypto.Hash, s *common.Snapshot, R crypto.Key) error {
	data := buildSnapshotAnnouncementMessage(s, R)
	return me.sendSnapshotMessageToPeer(idForNetwork, s.PayloadHash(), PeerMessageTypeSnapshotAnnouncement, data)
}

func (me *Peer) SendSnapshotCommitmentMessage(idForNetwork crypto.Hash, snap crypto.Hash, R crypto.Key, wantTx bool) error {
	data := buildSnapshotCommitmentMessage(snap, R, wantTx)
	return me.sendSnapshotMessageToPeer(idForNetwork, snap, PeerMessageTypeSnapshotCommitment, data)
}

func (me *Peer) SendTransactionChallengeMessage(idForNetwork crypto.Hash, snap crypto.Hash, cosi *crypto.CosiSignature, tx *common.VersionedTransaction) error {
	data := buildTransactionChallengeMessage(snap, cosi, tx)
	return me.sendSnapshotMessageToPeer(idForNetwork, snap, PeerMessageTypeTransactionChallenge, data)
}

func (me *Peer) SendFullChallengeMessage(idForNetwork crypto.Hash, s *common.Snapshot, commitment, challenge *crypto.Key, tx *common.VersionedTransaction) error {
	data := buildFullChanllengeMessage(s, commitment, challenge, tx)
	return me.sendSnapshotMessageToPeer(idForNetwork, s.PayloadHash(), PeerMessageTypeFullChallenge, data)
}

func (me *Peer) SendSnapshotResponseMessage(idForNetwork crypto.Hash, snap crypto.Hash, si *[32]byte) error {
	data := buildSnapshotResponseMessage(snap, si)
	return me.sendSnapshotMessageToPeer(idForNetwork, snap, PeerMessageTypeSnapshotResponse, data)
}

func (me *Peer) SendSnapshotFinalizationMessage(idForNetwork crypto.Hash, s *common.Snapshot) error {
	if idForNetwork == me.IdForNetwork {
		return nil
	}

	key := append(idForNetwork[:], s.Hash[:]...)
	key = append(key, 'S', 'C', 'O')
	if me.snapshotsCaches.contains(key, time.Hour) {
		return nil
	}

	data := buildSnapshotFinalizationMessage(s)
	return me.sendSnapshotMessageToPeer(idForNetwork, s.Hash, PeerMessageTypeSnapshotFinalization, data)
}

func (me *Peer) SendSnapshotConfirmMessage(idForNetwork crypto.Hash, snap crypto.Hash) error {
	key := append(idForNetwork[:], snap[:]...)
	key = append(key, 'S', 'N', 'A', 'P', PeerMessageTypeSnapshotConfirm)
	return me.sendHighToPeer(idForNetwork, PeerMessageTypeSnapshotConfirm, key, buildSnapshotConfirmMessage(snap))
}

func (me *Peer) SendTransactionRequestMessage(idForNetwork crypto.Hash, tx crypto.Hash) error {
	key := append(idForNetwork[:], tx[:]...)
	key = append(key, 'T', 'X', PeerMessageTypeTransactionRequest)
	return me.sendHighToPeer(idForNetwork, PeerMessageTypeTransactionRequest, key, buildTransactionRequestMessage(tx))
}

func (me *Peer) SendTransactionMessage(idForNetwork crypto.Hash, ver *common.VersionedTransaction) error {
	tx := ver.PayloadHash()
	key := append(idForNetwork[:], tx[:]...)
	key = append(key, 'T', 'X', PeerMessageTypeTransaction)
	return me.sendHighToPeer(idForNetwork, PeerMessageTypeTransaction, key, buildTransactionMessage(ver))
}

func (me *Peer) ConfirmSnapshotForPeer(idForNetwork, snap crypto.Hash) {
	key := append(idForNetwork[:], snap[:]...)
	key = append(key, 'S', 'C', 'O')
	me.snapshotsCaches.store(key, time.Now())
}

func buildAuthenticationMessage(data []byte) []byte {
	header := []byte{PeerMessageTypeAuthentication}
	return append(header, data...)
}

func buildGossipNeighborsMessage(neighbors []*Peer) []byte {
	rns := make([]string, len(neighbors))
	for i, p := range neighbors {
		rns[i] = p.Address
	}
	data := marshalPeers(rns)
	return append([]byte{PeerMessageTypeGossipNeighbors}, data...)
}

func buildSnapshotAnnouncementMessage(s *common.Snapshot, R crypto.Key) []byte {
	data := s.VersionedMarshal()
	data = append(R[:], data...)
	return append([]byte{PeerMessageTypeSnapshotAnnouncement}, data...)
}

func buildSnapshotCommitmentMessage(snap crypto.Hash, R crypto.Key, wantTx bool) []byte {
	data := []byte{PeerMessageTypeSnapshotCommitment}
	data = append(data, snap[:]...)
	data = append(data, R[:]...)
	if wantTx {
		return append(data, byte(1))
	}
	return append(data, byte(0))
}

func buildTransactionChallengeMessage(snap crypto.Hash, cosi *crypto.CosiSignature, tx *common.VersionedTransaction) []byte {
	data := []byte{PeerMessageTypeTransactionChallenge}
	data = append(data, snap[:]...)
	data = append(data, cosi.Signature[:]...)
	data = binary.BigEndian.AppendUint64(data, cosi.Mask)
	if tx != nil {
		pl := tx.Marshal()
		return append(data, pl...)
	}
	return data
}

func buildFullChanllengeMessage(s *common.Snapshot, commitment, challenge *crypto.Key, tx *common.VersionedTransaction) []byte {
	data := []byte{PeerMessageTypeFullChallenge}

	pl := s.VersionedMarshal()
	data = binary.BigEndian.AppendUint32(data, uint32(len(pl)))
	data = append(data, pl[:]...)

	data = append(data, commitment[:]...)
	data = append(data, challenge[:]...)

	pl = tx.Marshal()
	data = binary.BigEndian.AppendUint32(data, uint32(len(pl)))
	return append(data, pl...)
}

func buildSnapshotResponseMessage(snap crypto.Hash, si *[32]byte) []byte {
	data := []byte{PeerMessageTypeSnapshotResponse}
	data = append(data, snap[:]...)
	return append(data, si[:]...)
}

func buildSnapshotFinalizationMessage(s *common.Snapshot) []byte {
	data := s.VersionedMarshal()
	return append([]byte{PeerMessageTypeSnapshotFinalization}, data...)
}

func buildSnapshotConfirmMessage(snap crypto.Hash) []byte {
	return append([]byte{PeerMessageTypeSnapshotConfirm}, snap[:]...)
}

func buildTransactionMessage(ver *common.VersionedTransaction) []byte {
	data := ver.Marshal()
	return append([]byte{PeerMessageTypeTransaction}, data...)
}

func buildTransactionRequestMessage(tx crypto.Hash) []byte {
	return append([]byte{PeerMessageTypeTransactionRequest}, tx[:]...)
}

func buildGraphMessage(points []*SyncPoint) []byte {
	data := marshalSyncPoints(points)
	return append([]byte{PeerMessageTypeGraph}, data...)
}

func buildCommitmentsMessage(commitments []*crypto.Key) []byte {
	if len(commitments) > 1024 {
		panic(len(commitments))
	}
	data := []byte{PeerMessageTypeCommitments}
	data = binary.BigEndian.AppendUint16(data, uint16(len(commitments)))
	for _, k := range commitments {
		data = append(data, k[:]...)
	}
	return data
}

func buildBundleMessage(msgs []*ChanMsg) []byte {
	data := []byte{PeerMessageTypeBundle}
	for _, m := range msgs {
		if len(m.data) > TransportMessageMaxSize {
			panic(hex.EncodeToString(m.data))
		}
		data = binary.BigEndian.AppendUint32(data, uint32(len(m.data)))
		data = append(data, m.data...)
	}
	return data
}

func parseNetworkMessage(version uint8, data []byte) (*PeerMessage, error) {
	if len(data) < 1 {
		return nil, errors.New("invalid message data")
	}
	msg := &PeerMessage{Type: data[0]}
	switch msg.Type {
	case PeerMessageTypeCommitments:
		if len(data) < 3 {
			return nil, fmt.Errorf("invalid commitments message size %d", len(data))
		}
		count := binary.BigEndian.Uint16(data[1:3])
		if count > 1024 {
			return nil, fmt.Errorf("too much commitments %d", count)
		}
		if len(data[3:]) != int(count)*32 {
			return nil, fmt.Errorf("malformed commitments message %d %d", count, len(data[3:]))
		}
		for i := uint16(0); i < count; i++ {
			var key crypto.Key
			copy(key[:], data[3+32*i:])
			msg.Commitments = append(msg.Commitments, &key)
		}
	case PeerMessageTypeGraph:
		points, err := unmarshalSyncPoints(data[1:])
		if err != nil {
			return nil, err
		}
		msg.Graph = points
	case PeerMessageTypePing:
	case PeerMessageTypeGossipNeighbors:
		neighbors, err := unmarshalPeers(data[1:])
		if err != nil {
			return nil, err
		}
		msg.Neighbors = neighbors
	case PeerMessageTypeAuthentication:
		msg.Data = data[1:]
	case PeerMessageTypeSnapshotConfirm:
		copy(msg.SnapshotHash[:], data[1:])
	case PeerMessageTypeTransaction:
		ver, err := common.UnmarshalVersionedTransaction(data[1:])
		if err != nil {
			return nil, err
		}
		msg.Transaction = ver
	case PeerMessageTypeTransactionRequest:
		copy(msg.TransactionHash[:], data[1:])
	case PeerMessageTypeSnapshotAnnouncement:
		if len(data[1:]) <= 32 {
			return nil, fmt.Errorf("invalid announcement message size %d", len(data[1:]))
		}
		copy(msg.Commitment[:], data[1:])
		snap, err := common.UnmarshalVersionedSnapshot(data[33:])
		if err != nil {
			return nil, err
		}
		if snap == nil {
			return nil, fmt.Errorf("invalid snapshot announcement message data")
		}
		msg.Snapshot = snap.Snapshot
	case PeerMessageTypeSnapshotCommitment:
		if len(data[1:]) != 65 {
			return nil, fmt.Errorf("invalid commitment message size %d", len(data[1:]))
		}
		copy(msg.SnapshotHash[:], data[1:])
		copy(msg.Commitment[:], data[33:])
		msg.WantTx = data[65] == 1
	case PeerMessageTypeFullChallenge:
		if len(data[1:]) < 256 {
			return nil, fmt.Errorf("invalid full challenge message size %d", len(data[1:]))
		}
		offset := 1 + 4
		size := int(binary.BigEndian.Uint32(data[1:offset]))
		if len(data[offset:]) < size {
			return nil, fmt.Errorf("invalid full challenge snapshot size %d %d", len(data[offset:]), size)
		}
		s, err := common.UnmarshalVersionedSnapshot(data[offset : offset+size])
		if err != nil {
			return nil, fmt.Errorf("invalid full challenge snapshot %v", err)
		}
		msg.Snapshot = s.Snapshot
		offset = offset + size
		if len(data[offset:]) < 256 {
			return nil, fmt.Errorf("invalid full challenge message size %d %d", offset, len(data[offset:]))
		}
		msg.Cosi = *s.Snapshot.Signature
		msg.Snapshot.Signature = nil

		copy(msg.Commitment[:], data[offset:offset+32])
		offset = offset + 32
		copy(msg.Challenge[:], data[offset:offset+32])
		offset = offset + 32

		size = int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if len(data[offset:]) < size {
			return nil, fmt.Errorf("invalid full challenge transaction size %d %d", len(data[offset:]), size)
		}
		offset = offset + 4
		ver, err := common.UnmarshalVersionedTransaction(data[offset : offset+size])
		if err != nil {
			return nil, fmt.Errorf("invalid full challenge transaction %v", err)
		}
		msg.Transaction = ver
	case PeerMessageTypeTransactionChallenge:
		if len(data[1:]) < 104 {
			return nil, fmt.Errorf("invalid transaction challenge message size %d", len(data[1:]))
		}
		copy(msg.SnapshotHash[:], data[1:])
		copy(msg.Cosi.Signature[:], data[33:])
		msg.Cosi.Mask = binary.BigEndian.Uint64(data[97:105])
		if len(data[1:]) > 104 {
			ver, err := common.UnmarshalVersionedTransaction(data[105:])
			if err != nil {
				return nil, err
			}
			msg.Transaction = ver
		}
	case PeerMessageTypeSnapshotResponse:
		if len(data[1:]) != 64 {
			return nil, fmt.Errorf("invalid response message size %d", len(data[1:]))
		}
		copy(msg.SnapshotHash[:], data[1:])
		copy(msg.Response[:], data[33:])
	case PeerMessageTypeSnapshotFinalization:
		snap, err := common.UnmarshalVersionedSnapshot(data[1:])
		if err != nil {
			return nil, err
		}
		if snap == nil {
			return nil, fmt.Errorf("invalid snapshot finalization message data")
		}
		msg.Snapshot = snap.Snapshot
	case PeerMessageTypeBundle:
		msg.Data = data[1:]
	}
	return msg, nil
}

func (me *Peer) handlePeerMessage(peer *Peer, receive chan *PeerMessage) {
	for msg := range receive {
		switch msg.Type {
		case PeerMessageTypePing:
		case PeerMessageTypeGossipNeighbors:
			if me.gossipNeighbors {
				me.handle.UpdateNeighbors(msg.Neighbors)
			}
		case PeerMessageTypeCommitments:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeCommitments %s %d\n", peer.IdForNetwork, len(msg.Commitments))
			me.handle.CosiQueueExternalCommitments(peer.IdForNetwork, msg.Commitments)
		case PeerMessageTypeGraph:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeGraph %s\n", peer.IdForNetwork)
			me.handle.UpdateSyncPoint(peer.IdForNetwork, msg.Graph)
			peer.syncRing.Offer(msg.Graph)
		case PeerMessageTypeTransactionRequest:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeTransactionRequest %s %s\n", peer.IdForNetwork, msg.TransactionHash)
			me.handle.SendTransactionToPeer(peer.IdForNetwork, msg.TransactionHash)
		case PeerMessageTypeTransaction:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeTransaction %s\n", peer.IdForNetwork)
			me.handle.CachePutTransaction(peer.IdForNetwork, msg.Transaction)
		case PeerMessageTypeSnapshotConfirm:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeSnapshotConfirm %s %s\n", peer.IdForNetwork, msg.SnapshotHash)
			me.ConfirmSnapshotForPeer(peer.IdForNetwork, msg.SnapshotHash)
		case PeerMessageTypeSnapshotAnnouncement:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeSnapshotAnnouncement %s %s\n", peer.IdForNetwork, msg.Snapshot.SoleTransaction())
			me.handle.CosiQueueExternalAnnouncement(peer.IdForNetwork, msg.Snapshot, &msg.Commitment)
		case PeerMessageTypeSnapshotCommitment:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeSnapshotCommitment %s %s\n", peer.IdForNetwork, msg.SnapshotHash)
			me.handle.CosiAggregateSelfCommitments(peer.IdForNetwork, msg.SnapshotHash, &msg.Commitment, msg.WantTx)
		case PeerMessageTypeTransactionChallenge:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeTransactionChallenge %s %s %t\n", peer.IdForNetwork, msg.SnapshotHash, msg.Transaction != nil)
			me.handle.CosiQueueExternalChallenge(peer.IdForNetwork, msg.SnapshotHash, &msg.Cosi, msg.Transaction)
		case PeerMessageTypeFullChallenge:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeFullChallenge %s %v %v\n", peer.IdForNetwork, msg.Snapshot, msg.Transaction)
			me.handle.CosiQueueExternalFullChallenge(peer.IdForNetwork, msg.Snapshot, &msg.Commitment, &msg.Challenge, &msg.Cosi, msg.Transaction)
		case PeerMessageTypeSnapshotResponse:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeSnapshotResponse %s %s\n", peer.IdForNetwork, msg.SnapshotHash)
			me.handle.CosiAggregateSelfResponses(peer.IdForNetwork, msg.SnapshotHash, &msg.Response)
		case PeerMessageTypeSnapshotFinalization:
			logger.Verbosef("network.handle handlePeerMessage PeerMessageTypeSnapshotFinalization %s %s\n", peer.IdForNetwork, msg.Snapshot.SoleTransaction())
			me.handle.VerifyAndQueueAppendSnapshotFinalization(peer.IdForNetwork, msg.Snapshot)
		}
	}
}

func marshalSyncPoints(points []*SyncPoint) []byte {
	enc := common.NewMinimumEncoder()
	enc.WriteInt(len(points))
	for _, p := range points {
		enc.Write(p.NodeId[:])
		enc.WriteUint64(p.Number)
		enc.Write(p.Hash[:])
	}
	return enc.Bytes()
}

func unmarshalSyncPoints(b []byte) ([]*SyncPoint, error) {
	dec, err := common.NewMinimumDecoder(b)
	if err != nil {
		return nil, err
	}
	count, err := dec.ReadInt()
	if err != nil {
		return nil, err
	}
	points := make([]*SyncPoint, count)
	for i := range points {
		p := &SyncPoint{}
		err = dec.Read(p.NodeId[:])
		if err != nil {
			return nil, err
		}
		num, err := dec.ReadUint64()
		if err != nil {
			return nil, err
		}
		p.Number = num
		err = dec.Read(p.Hash[:])
		if err != nil {
			return nil, err
		}
		points[i] = p
	}
	return points, nil
}

func marshalPeers(peers []string) []byte {
	enc := common.NewMinimumEncoder()
	enc.WriteInt(len(peers))
	for _, p := range peers {
		enc.WriteInt(len(p))
		enc.Write([]byte(p))
	}
	return enc.Bytes()
}

func unmarshalPeers(b []byte) ([]string, error) {
	dec, err := common.NewMinimumDecoder(b)
	if err != nil {
		return nil, err
	}
	count, err := dec.ReadInt()
	if err != nil {
		return nil, err
	}
	peers := make([]string, count)
	for i := range peers {
		as, err := dec.ReadInt()
		if err != nil {
			return nil, err
		}
		addr := make([]byte, as)
		err = dec.Read(addr)
		if err != nil {
			return nil, err
		}
		peers[i] = string(addr)
	}
	return peers, nil
}
