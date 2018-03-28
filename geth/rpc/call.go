package rpc

import (
	"errors"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	txutils "github.com/status-im/status-go/geth/transactions/utils"
)

// Call represents a unit of a rpc request which is to be executed.
type Call struct {
	ID     int64
	Method string
	Params []interface{}
}

// contains series of errors for parsing operations.
var (
	ErrInvalidFromAddress = errors.New("Failed to parse From Address")
	ErrInvalidToAddress   = errors.New("Failed to parse To Address")
)

// ParseFromAddress returns the address associated with the Call.
func (r Call) ParseFromAddress() (gethcommon.Address, error) {
	params, ok := r.Params[0].(map[string]interface{})
	if !ok {
		return gethcommon.HexToAddress("0x"), ErrInvalidFromAddress
	}

	from, ok := params["from"].(string)
	if !ok {
		return gethcommon.HexToAddress("0x"), ErrInvalidFromAddress
	}

	return gethcommon.HexToAddress(from), nil
}

// ParseToAddress returns the gethcommon.Address associated with the call.
func (r Call) ParseToAddress() (gethcommon.Address, error) {
	params, ok := r.Params[0].(map[string]interface{})
	if !ok {
		return gethcommon.HexToAddress("0x"), ErrInvalidToAddress
	}

	to, ok := params["to"].(string)
	if !ok {
		return gethcommon.HexToAddress("0x"), ErrInvalidToAddress
	}

	return gethcommon.HexToAddress(to), nil
}

// ParseData returns the bytes associated with the call.
func (r Call) ParseData() hexutil.Bytes {
	params, ok := r.Params[0].(map[string]interface{})
	if !ok {
		return hexutil.Bytes("0x")
	}

	data, ok := params["data"].(string)
	if !ok {
		data = "0x"
	}

	byteCode, err := hexutil.Decode(data)
	if err != nil {
		byteCode = hexutil.Bytes(data)
	}

	return byteCode
}

// ParseValue returns the hex big associated with the call.
// nolint: dupl
func (r Call) ParseValue() *hexutil.Big {
	params, ok := r.Params[0].(map[string]interface{})
	if !ok {
		return nil
		//return (*hexutil.Big)(big.NewInt("0x0"))
	}

	inputValue, ok := params["value"].(string)
	if !ok {
		return nil
	}

	parsedValue, err := hexutil.DecodeBig(inputValue)
	if err != nil {
		return nil
	}

	return (*hexutil.Big)(parsedValue)
}

// ParseGas returns the hex big associated with the call.
// nolint: dupl
func (r Call) ParseGas() *hexutil.Uint64 {
	params, ok := r.Params[0].(map[string]interface{})
	if !ok {
		return nil
	}

	inputValue, ok := params["gas"].(string)
	if !ok {
		return nil
	}

	parsedValue, err := hexutil.DecodeUint64(inputValue)
	if err != nil {
		return nil
	}

	v := hexutil.Uint64(parsedValue)
	return &v
}

// ParseGasPrice returns the hex big associated with the call.
// nolint: dupl
func (r Call) ParseGasPrice() *hexutil.Big {
	params, ok := r.Params[0].(map[string]interface{})
	if !ok {
		return nil
	}

	inputValue, ok := params["gasPrice"].(string)
	if !ok {
		return nil
	}

	parsedValue, err := hexutil.DecodeBig(inputValue)
	if err != nil {
		return nil
	}

	return (*hexutil.Big)(parsedValue)
}

// ToSendTxArgs converts Call to SendTxArgs.
func (r Call) ToSendTxArgs() txutils.SendTxArgs {
	var err error
	var fromAddr, toAddr gethcommon.Address

	fromAddr, err = r.ParseFromAddress()
	if err != nil {
		fromAddr = gethcommon.HexToAddress("0x0")
	}

	toAddr, err = r.ParseToAddress()
	if err != nil {
		toAddr = gethcommon.HexToAddress("0x0")
	}

	input := r.ParseData()
	return txutils.SendTxArgs{
		To:       &toAddr,
		From:     fromAddr,
		Value:    r.ParseValue(),
		Input:    input,
		Gas:      r.ParseGas(),
		GasPrice: r.ParseGasPrice(),
	}
}
