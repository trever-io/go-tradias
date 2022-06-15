package tradias

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"
)

type LiquidityBook struct {
	Sells [][]string
	Buys  [][]string
}

type TickerEntry struct {
	Quantity decimal.Decimal `json:"quantity"`
	Price    decimal.Decimal `json:"price"`
}

type TickerResponse struct {
	Buy  []TickerEntry `json:"buy"`
	Sell []TickerEntry `json:"sell"`
}

const PRICES_CHANNEL_NAME = "prices"

func (c *APIClient) GetTicker(ctx context.Context, baseAsset string, quoteAsset string) (*LiquidityBook, error) {
	symbol := fmt.Sprintf("%v%v", baseAsset, quoteAsset)
	sub := subscribeMessage{
		Type:        TRADIAS_SUBSCRIPTION_TYPE,
		ChannelName: PRICES_CHANNEL_NAME,
		Instrument:  symbol,
	}

	conn, err := c.connect(ctx, &sub)

	book, err := getLiquidityBookSnapshot(conn)
	if err != nil {
		return nil, err
	}

	return book, nil

}

func getLiquidityBookSnapshot(conn *websocket.Conn) (*LiquidityBook, error) {
	for {
		_, data, err := conn.ReadMessage()
		if err == io.EOF {
			return nil, err
		}
		if err != nil {
			tmp := fmt.Errorf("error during websocket connection: %w", err)
			fmt.Println(tmp)
			return nil, tmp
		}

		msg := new(websocketResponse)
		err = json.Unmarshal(data, msg)
		if err != nil {
			return nil, err
		}

		if msg.Event == nil {
			continue
		}

		var book *LiquidityBook
		if *msg.Event == "prices" {
			buy := msg.Levels.Buy
			sell := msg.Levels.Sell

			buys := getLiquidityEntries(buy)
			sells := getLiquidityEntries(sell)

			book = &LiquidityBook{Buys: buys, Sells: sells}
		} else {
			return nil, fmt.Errorf("unexpected event: %v", msg.Event)
		}

		return book, nil
	}
}

func getLiquidityEntries(tickerResp []TickerEntry) [][]string {
	lb := make([][]string, len(tickerResp))
	for i, entry := range tickerResp {
		lb[i] = append(lb[i], entry.Price.String())
		lb[i] = append(lb[i], entry.Quantity.String())
	}

	return lb
}
