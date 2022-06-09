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

type subscribeMessage struct {
	Type        string `json:"type"`
	ChannelName string `json:"channelname"`
	Instrument  string `json:"instrument"`
	Heartbeat   bool   `json:"heartbeat"`
}

type TickerEntry struct {
	Quantity decimal.Decimal `json:"quantity"`
	Price    decimal.Decimal `json:"price"`
}

type TickerResponse struct {
	Buy  []TickerEntry `json:"buy"`
	Sell []TickerEntry `json:"sell"`
}

type websocketResponse struct {
	Event  string         `json:"event"`
	Type   string         `json:"type"`
	Levels TickerResponse `json:"levels"`
}

const TRADIAS_WEBSOCKET_CHANNEL = "wss://otcapp-uat.tradias.de/otc/ws"
const TRADIAS_SUBSCRIPTION_TYPE = "subscribe"
const TRADIAS_CHANNEL_NAME = "prices"
const TRADIAS_HEARTBEAT_EVENT = "heartbeat"
const TRADIAS_SUBSCRIPTION_TYPE_STATUS = "subscribed"

func (a *APIClient) GetTicker(baseAsset string, quoteAsset string) (*LiquidityBook, error) {
	header := map[string][]string{"x-token-id": {a.apiKey}}
	conn, _, err := websocket.DefaultDialer.DialContext(context.TODO(), TRADIAS_WEBSOCKET_CHANNEL, header)
	if err != nil {
		return nil, err
	}

	symbol := fmt.Sprintf("%v%v", baseAsset, quoteAsset)
	sub := subscribeMessage{
		Type:        TRADIAS_SUBSCRIPTION_TYPE,
		ChannelName: TRADIAS_CHANNEL_NAME,
		Instrument:  symbol,
		Heartbeat:   true,
	}

	err = conn.WriteJSON(sub)
	if err != nil {
		conn.Close()
		return nil, err
	}

	err = getSubscriptionResult(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}

	book, err := getLiquidityBookSnapshot(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}

	return book, nil

}

func getSubscriptionResult(conn *websocket.Conn) error {
	for {
		fmt.Println("Receiving data from websocket...")
		_, data, err := conn.ReadMessage()
		if err == io.EOF {
			conn.Close()
			return err
		}
		if err != nil {
			fmt.Printf("error during websocket connection: %v\n", err)
			conn.Close()
			return err
		}

		msg := new(websocketResponse)
		err = json.Unmarshal(data, msg)
		if err != nil {
			return err
		}

		switch msg.Type {
		case TRADIAS_HEARTBEAT_EVENT:
			fallthrough
		case TRADIAS_SUBSCRIPTION_TYPE:
			continue
		case TRADIAS_SUBSCRIPTION_TYPE_STATUS:
			return nil
		case "":
			continue
		default:
			return fmt.Errorf("unhandled type: %v", msg.Type)
		}
	}
}

func getLiquidityBookSnapshot(conn *websocket.Conn) (*LiquidityBook, error) {
	for {
		_, data, err := conn.ReadMessage()
		if err == io.EOF {
			conn.Close()
			return nil, err
		}
		if err != nil {
			fmt.Printf("error during websocket connection: %v\n", err)
			conn.Close()
			return nil, err
		}

		msg := new(websocketResponse)
		err = json.Unmarshal(data, msg)
		if err != nil {
			return nil, err
		}

		var book *LiquidityBook
		if msg.Event == "prices" {
			buy := msg.Levels.Buy
			sell := msg.Levels.Sell

			buys := getLiquidityEntries(buy)
			sells := getLiquidityEntries(sell)

			book = &LiquidityBook{Buys: buys, Sells: sells}
		} else if msg.Event == "" {
			continue
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
