package bitchan_pb

import (
	"crypto/sha256"
	"golang.org/x/crypto/ripemd160"
	"github.com/golang/protobuf/proto"
	"log"
	"bytes"
)

// TODO(tetsui): To []byte instead of that
type BlockHash [20]byte
type BlockMiningHash [32]byte
type BlockBodyHash [20]byte
type TransactionHash [20]byte
type PostHash [20]byte

type Block struct {
	BlockHeader
	BlockBody
	Transactions []*Transaction
}

func (b *Block) UpdateBodyHash() {
	for _, transaction := range b.Transactions {
		transactionHash := transaction.Hash()
		b.BlockBody.TransactionHashes = append(
			b.BlockBody.TransactionHashes, transactionHash[:])
	}

	// TODO(tetsui): Use merkle tree to hash.
	bodyHash := b.BlockBody.Hash()
	b.BlockHeader.BodyHash = bodyHash[:]
}

func Hash160(data []byte) []byte {
	hs := sha256.New()
	hs.Write(data)
	data = hs.Sum(nil)
	hr := ripemd160.New()
	hr.Write(data)
	return hr.Sum(nil)
}

func Hash256(data []byte) []byte {
	h := sha256.New()
	h.Write(data)
	data = h.Sum(nil)
	h = sha256.New()
	h.Write(data)
	return h.Sum(nil)
}

// TODO(tetsui): Change to RIPEMD160(SHA256(P))

func (bh *BlockHeader) Hash() BlockHash {
	data, err := proto.Marshal(bh)
	if err != nil {
		log.Fatal("marshaling error:", err)
	}
	var res BlockHash
	copy(res[:], Hash160(data))
	return res
}

func (bh *BlockHeader) MiningHash() BlockMiningHash {
	data, err := proto.Marshal(bh)
	if err != nil {
		log.Fatal("marshaling error:", err)
	}
	var res BlockMiningHash
	copy(res[:], Hash256(data))
	return res
}

func (bb *BlockBody) Hash() BlockBodyHash {
	data, err := proto.Marshal(bb)
	if err != nil {
		log.Fatal("marshaling error:", err)
	}
	var res BlockBodyHash
	copy(res[:], Hash160(data))
	return res
}

func (t *Transaction) Hash() TransactionHash {
	data, err := proto.Marshal(t)
	if err != nil {
		log.Fatal("marshaling error:", err)
	}
	var res TransactionHash
	copy(res[:], Hash160(data))
	return res
}

func (p *Post) Hash() PostHash {
	data, err := proto.Marshal(p)
	if err != nil {
		log.Fatal("marshaling error:", err)
	}
	var res PostHash
	copy(res[:], Hash160(data))
	return res
}

func (t *Transaction) IsInThread(threadHash TransactionHash) bool {
	if len(t.PostHash) == 0 {
		return false
	}
	if len(t.ThreadTransactionHash) > 0 {
		return bytes.Equal(t.ThreadTransactionHash, threadHash[:])
	} else {
		currentHash := t.Hash()
		return bytes.Equal(currentHash[:], threadHash[:])
	}
}
