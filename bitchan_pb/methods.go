package bitchan_pb

import (
	"crypto/sha256"
	"golang.org/x/crypto/ripemd160"
	"github.com/golang/protobuf/proto"
	"log"
	"bytes"
)

type BlockHash [32]byte
type BlockBodyHash [32]byte
type TransactionHash [20]byte
type PostHash [20]byte

type Block struct {
	BlockHeader
	BlockBody
}

func (b *Block) UpdateBodyHash() {
	bodyHash := b.BlockBody.Hash()
	b.BlockHeader.BodyHash = bodyHash[:]
}

// TODO(tetsui): Change to SHA256(SHA256(P)) or RIPEMD160(SHA256(P))

func (bh *BlockHeader) Hash() BlockHash {
	data, err := proto.Marshal(bh)
	if err != nil {
		log.Fatal("marshaling error:", err)
	}
	h := sha256.New()
	h.Write(data)
	var res BlockHash
	copy(res[:], h.Sum(nil))
	return res
}

func (bb *BlockBody) Hash() BlockBodyHash {
	data, err := proto.Marshal(bb)
	if err != nil {
		log.Fatal("marshaling error:", err)
	}
	h := sha256.New()
	h.Write(data)
	var res BlockBodyHash
	copy(res[:], h.Sum(nil))
	return res
}

func (t *Transaction) Hash() TransactionHash {
	data, err := proto.Marshal(t)
	if err != nil {
		log.Fatal("marshaling error:", err)
	}
	h := ripemd160.New()
	h.Write(data)
	var res TransactionHash
	copy(res[:], h.Sum(nil))
	return res
}

func (p *Post) Hash() PostHash {
	data, err := proto.Marshal(p)
	if err != nil {
		log.Fatal("marshaling error:", err)
	}
	h := ripemd160.New()
	h.Write(data)
	var res PostHash
	copy(res[:], h.Sum(nil))
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
