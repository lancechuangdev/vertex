package ethrpc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
)

type Client struct {
	url        string
	httpClient *http.Client
}

const maxResponseBytes = 32 << 20

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type Block struct {
	Number       uint64
	Hash         string
	ParentHash   string
	Timestamp    uint64
	Transactions []Transaction
}

type Transaction struct {
	Hash  string
	Index uint64
	From  string
	To    *string
	Value string
}

type Receipt struct {
	TransactionHash string
	BlockNumber     uint64
	Status          *uint64
	GasUsed         uint64
	ContractAddress *string
	Logs            []Log
}

type Log struct {
	Index   uint64
	Address string
	Topics  []string
	Data    string
}

type rpcBlock struct {
	Number       string           `json:"number"`
	Hash         string           `json:"hash"`
	ParentHash   string           `json:"parentHash"`
	Timestamp    string           `json:"timestamp"`
	Transactions []rpcTransaction `json:"transactions"`
}

type rpcTransaction struct {
	Hash             string  `json:"hash"`
	TransactionIndex string  `json:"transactionIndex"`
	From             string  `json:"from"`
	To               *string `json:"to"`
	Value            string  `json:"value"`
}

type rpcReceipt struct {
	TransactionHash string   `json:"transactionHash"`
	BlockNumber     string   `json:"blockNumber"`
	Status          *string  `json:"status"`
	GasUsed         string   `json:"gasUsed"`
	ContractAddress *string  `json:"contractAddress"`
	Logs            []rpcLog `json:"logs"`
}

type rpcLog struct {
	LogIndex string   `json:"logIndex"`
	Address  string   `json:"address"`
	Topics   []string `json:"topics"`
	Data     string   `json:"data"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func New(url string, httpClient *http.Client) *Client {
	return &Client{url: url, httpClient: httpClient}
}

func (c *Client) ChainID(ctx context.Context) (uint64, error) {
	return c.hexUint64(ctx, "eth_chainId", nil)
}

func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	return c.hexUint64(ctx, "eth_blockNumber", nil)
}

func (c *Client) BlockByNumber(ctx context.Context, number uint64) (Block, error) {
	var value *rpcBlock
	if err := c.call(ctx, "eth_getBlockByNumber", []any{fmt.Sprintf("0x%x", number), true}, &value); err != nil {
		return Block{}, err
	}
	if value == nil {
		return Block{}, fmt.Errorf("eth_getBlockByNumber returned no block for %d", number)
	}
	returnedNumber, err := parseHexUint64("block number", value.Number)
	if err != nil {
		return Block{}, err
	}
	if returnedNumber != number {
		return Block{}, fmt.Errorf("requested block %d, node returned %d", number, returnedNumber)
	}
	timestamp, err := parseHexUint64("block timestamp", value.Timestamp)
	if err != nil {
		return Block{}, err
	}
	if !validHash(value.Hash) || !validHash(value.ParentHash) {
		return Block{}, fmt.Errorf("block %d returned an invalid hash", number)
	}
	block := Block{Number: number, Hash: strings.ToLower(value.Hash), ParentHash: strings.ToLower(value.ParentHash), Timestamp: timestamp}
	block.Transactions = make([]Transaction, 0, len(value.Transactions))
	for _, raw := range value.Transactions {
		transaction, err := decodeTransaction(raw)
		if err != nil {
			return Block{}, fmt.Errorf("decode block %d transaction: %w", number, err)
		}
		block.Transactions = append(block.Transactions, transaction)
	}
	return block, nil
}

func (c *Client) TransactionReceipt(ctx context.Context, hash string) (Receipt, error) {
	if !validHash(hash) {
		return Receipt{}, fmt.Errorf("invalid transaction hash %q", hash)
	}
	var value *rpcReceipt
	if err := c.call(ctx, "eth_getTransactionReceipt", []any{hash}, &value); err != nil {
		return Receipt{}, err
	}
	if value == nil {
		return Receipt{}, fmt.Errorf("eth_getTransactionReceipt returned no receipt for %s", hash)
	}
	return decodeReceipt(*value)
}

func decodeTransaction(raw rpcTransaction) (Transaction, error) {
	if !validHash(raw.Hash) {
		return Transaction{}, fmt.Errorf("invalid hash %q", raw.Hash)
	}
	index, err := parseHexUint64("transaction index", raw.TransactionIndex)
	if err != nil {
		return Transaction{}, err
	}
	from, err := normalizeAddress(raw.From)
	if err != nil {
		return Transaction{}, fmt.Errorf("from: %w", err)
	}
	to, err := normalizeOptionalAddress(raw.To)
	if err != nil {
		return Transaction{}, fmt.Errorf("to: %w", err)
	}
	value, err := parseHexQuantity("transaction value", raw.Value)
	if err != nil {
		return Transaction{}, err
	}
	return Transaction{Hash: strings.ToLower(raw.Hash), Index: index, From: from, To: to, Value: value}, nil
}

func decodeReceipt(raw rpcReceipt) (Receipt, error) {
	if !validHash(raw.TransactionHash) {
		return Receipt{}, fmt.Errorf("invalid receipt transaction hash %q", raw.TransactionHash)
	}
	blockNumber, err := parseHexUint64("receipt block number", raw.BlockNumber)
	if err != nil {
		return Receipt{}, err
	}
	gasUsed, err := parseHexUint64("receipt gas used", raw.GasUsed)
	if err != nil {
		return Receipt{}, err
	}
	contractAddress, err := normalizeOptionalAddress(raw.ContractAddress)
	if err != nil {
		return Receipt{}, fmt.Errorf("contract address: %w", err)
	}
	var status *uint64
	if raw.Status != nil {
		parsed, err := parseHexUint64("receipt status", *raw.Status)
		if err != nil || parsed > 1 {
			return Receipt{}, fmt.Errorf("invalid receipt status %q", *raw.Status)
		}
		status = &parsed
	}
	receipt := Receipt{TransactionHash: strings.ToLower(raw.TransactionHash), BlockNumber: blockNumber, Status: status, GasUsed: gasUsed, ContractAddress: contractAddress}
	receipt.Logs = make([]Log, 0, len(raw.Logs))
	for _, rawLog := range raw.Logs {
		logIndex, err := parseHexUint64("log index", rawLog.LogIndex)
		if err != nil {
			return Receipt{}, err
		}
		address, err := normalizeAddress(rawLog.Address)
		if err != nil {
			return Receipt{}, fmt.Errorf("log address: %w", err)
		}
		topics := make([]string, len(rawLog.Topics))
		for i, topic := range rawLog.Topics {
			if !validHash(topic) {
				return Receipt{}, fmt.Errorf("invalid log topic %q", topic)
			}
			topics[i] = strings.ToLower(topic)
		}
		if !validData(rawLog.Data) {
			return Receipt{}, fmt.Errorf("invalid log data %q", rawLog.Data)
		}
		receipt.Logs = append(receipt.Logs, Log{Index: logIndex, Address: address, Topics: topics, Data: strings.ToLower(rawLog.Data)})
	}
	return receipt, nil
}

func (c *Client) hexUint64(ctx context.Context, method string, params []any) (uint64, error) {
	var value string
	if err := c.call(ctx, method, params, &value); err != nil {
		return 0, err
	}
	return parseHexUint64(method+" result", value)
}

func parseHexUint64(name, value string) (uint64, error) {
	if !strings.HasPrefix(value, "0x") || len(value) <= 2 {
		return 0, fmt.Errorf("%s is invalid hex quantity %q", name, value)
	}
	n, err := strconv.ParseUint(value[2:], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, value, err)
	}
	return n, nil
}

func parseHexQuantity(name, value string) (string, error) {
	if !strings.HasPrefix(value, "0x") || len(value) <= 2 {
		return "", fmt.Errorf("%s is invalid hex quantity %q", name, value)
	}
	n, ok := new(big.Int).SetString(value[2:], 16)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return "", fmt.Errorf("%s is invalid uint256 quantity %q", name, value)
	}
	return n.String(), nil
}

func normalizeAddress(value string) (string, error) {
	if len(value) != 42 || !strings.HasPrefix(value, "0x") {
		return "", fmt.Errorf("invalid address %q", value)
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return "", fmt.Errorf("invalid address %q", value)
	}
	return strings.ToLower(value), nil
}

func normalizeOptionalAddress(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized, err := normalizeAddress(*value)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func validData(value string) bool {
	if !strings.HasPrefix(value, "0x") || len(value)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

func validHash(value string) bool {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

func (c *Client) call(ctx context.Context, method string, params []any, result any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(request{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("call %s: HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(limited)))
	}

	var envelope response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("call %s: RPC error %d: %s", method, envelope.Error.Code, envelope.Error.Message)
	}
	if len(envelope.Result) == 0 {
		return fmt.Errorf("call %s: response has no result", method)
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}
