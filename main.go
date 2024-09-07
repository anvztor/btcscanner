package main

// ... Existing imports ...
import (
	"encoding/binary"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/syndtr/goleveldb/leveldb"
)

func main() {
	connConfig := &rpcclient.ConnConfig{
		Host:         "localhost:18443",
		User:         "btc",
		Pass:         "btc",
		HTTPPostMode: true, // Bitcoin core only supports HTTP POST mode
		DisableTLS:   true, // Disable TLS for simplicity (only if not using TLS)
	}

	client, err := rpcclient.New(connConfig, nil)
	if err != nil {
		log.Fatalf("Failed to start Bitcoin client: %v", err)
	}
	defer client.Shutdown()

	// Try to get blockchain information
	info, err := client.GetBlockChainInfo()
	if err != nil {
		log.Fatal("Error getting blockchain information:", err)
	}
	fmt.Printf("Current block height: %d\n", info.Blocks)

	// Open or create the local storage (LevelDB)
	dbPath := filepath.Join("btc_cache.db")
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		log.Fatalf("Failed to open local storage: %v", err)
	}
	defer db.Close()

	listenAndCacheBTCBlocks(client, db)
	go func() {
		for range time.NewTicker(24 * time.Hour).C {
			purgeOldData(db)
		}
	}()

	select {}
}

func listenAndCacheBTCBlocks(client *rpcclient.Client, db *leveldb.DB) {
	currentHeight := 110
	for {
		// Get the current block hash
		blockHash, err := client.GetBlockHash(int64(currentHeight))
		if err != nil {
			log.Printf("Error getting block hash: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// Get the block
		msgBlock, err := client.GetBlock(blockHash)
		if err != nil {
			log.Printf("Error getting block: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		// Convert to *btcutil.Block
		block := btcutil.NewBlock(msgBlock)

		// Cache block data
		cacheBlockData(db, block)

		log.Printf("Cached block height: %d", currentHeight)

		// Move to the next block
		currentHeight++

		// Check if the latest block has been reached
		bestHeight, err := client.GetBlockCount()
		if err != nil {
			log.Printf("Error getting latest block height: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		if int64(currentHeight) > bestHeight {
			log.Printf("Reached the latest block, waiting for new blocks...")
			time.Sleep(10 * time.Second)
			currentHeight = int(bestHeight)
		}
	}
}

func cacheBlockData(db *leveldb.DB, block *btcutil.Block) {
	blockHash := block.Hash().String()
	header := block.MsgBlock().Header
	difficulty := header.Bits
	randomNumber := header.Nonce
	merkleRoot := header.MerkleRoot.String()
	blockTime := header.Timestamp.Unix()

	// Manual formatting of header fields
	headerStr := fmt.Sprintf("Version: %d, PrevBlock: %s, MerkleRoot: %s, Timestamp: %d, Bits: %d, Nonce: %d",
		header.Version, header.PrevBlock, header.MerkleRoot, header.Timestamp.Unix(), header.Bits, header.Nonce)

	// Convert to little-endian and store
	difficultyLE := make([]byte, 4)
	randomNumberLE := make([]byte, 4)
	blockTimeLE := make([]byte, 8)

	binary.LittleEndian.PutUint32(difficultyLE, difficulty)
	binary.LittleEndian.PutUint32(randomNumberLE, randomNumber)
	binary.LittleEndian.PutUint64(blockTimeLE, uint64(blockTime))

	// Cache block header
	db.Put([]byte("header:"+blockHash), []byte(headerStr), nil)

	// Cache difficulty in little-endian
	db.Put([]byte("difficulty:"+blockHash), difficultyLE, nil)

	// Cache random number in little-endian
	db.Put([]byte("random:"+blockHash), randomNumberLE, nil)

	// Cache Merkle root
	db.Put([]byte("merkleroot:"+blockHash), []byte(merkleRoot), nil)

	// Cache block time in little-endian
	db.Put([]byte("blocktime:"+blockHash), blockTimeLE, nil)

	// Cache block hash
	db.Put([]byte("blockhash:"+blockHash), []byte(blockHash), nil)

	// Cache UTXOs (Simplified example, more details should be stored in real case)
	for _, tx := range block.Transactions() {
		for _, txOut := range tx.MsgTx().TxOut {
			utxoKey := fmt.Sprintf("utxo:%s:%d", blockHash, txOut.Value)
			db.Put([]byte(utxoKey), txOut.PkScript, nil)
		}
	}

	log.Printf("Cached block %s with header %s, difficulty %d, random number %d, Merkle root %s, and block time %d",
		blockHash, headerStr, difficulty, randomNumber, merkleRoot, blockTime)
}

func purgeOldData(db *leveldb.DB) {
	thresholdTime := time.Now().AddDate(0, 0, -3).Unix()

	iter := db.NewIterator(nil, nil)
	defer iter.Release()

	for iter.Next() {
		key := iter.Key()
		blockTimeBytes, err := db.Get([]byte("blocktime:"+string(key[len("blockhash:"):])), nil)
		if err != nil {
			log.Printf("Error getting block time: %v", err)
			continue
		}

		blockTime := int64(binary.LittleEndian.Uint64(blockTimeBytes))
		if blockTime < thresholdTime {
			db.Delete(key, nil)
			log.Printf("Deleted block with key: %s", key)
		}
	}
	if err := iter.Error(); err != nil {
		log.Printf("Error during data purge: %v", err)
	}
}
