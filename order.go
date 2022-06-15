package tradias

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type OrderType string

var (
	OrderType_MARKET OrderType = "MARKET"
)

type Side string

var (
	Side_BUY  = "BUY"
	Side_SELL = "SELL"
)

const ORDERS_CHANNEL_NAME = "orders"
const CREATE_ORDER_TYPE = "CREATE_ORDER"
const ORDER_EVENT = "orders"

var (
	ErrOrderChannelNil       = errors.New("order channel is nil")
	ErrCallbackChannelClosed = errors.New("callback channel was closed")
)

type Order struct {
	Instrument    string      `json:"instrument"`
	OrderType     OrderType   `json:"order_type"`
	Side          Side        `json:"side"`
	Amount        json.Number `json:"amount"`
	Currency      string      `json:"currency,omitempty"`
	ClientOrderId string      `json:"client_order_id,omitempty"`

	// for internal use only
	InternalClientOrderId string `json:"client_order_id_1"`
}

func ToInstrument(baseAsset string, quoteAsset string) string {
	return fmt.Sprintf("%v%v", baseAsset, quoteAsset)
}

func (c *APIClient) CreateOrder(ctx context.Context, order *Order) (*Order, error) {
	if c.createOrderChan == nil {
		return nil, ErrOrderChannelNil
	}

	cb := make(chan *createOrderResponse, 1)
	order.InternalClientOrderId = uuid.New().String()
	req := &createOrderRequest{
		cb:    cb,
		order: order,
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case c.createOrderChan <- req:
	}

	var resp *createOrderResponse
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp = <-cb:
	}

	if resp == nil {
		return nil, ErrCallbackChannelClosed
	}

	return resp.order, resp.err
}
