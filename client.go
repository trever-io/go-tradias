package tradias

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/gorilla/websocket"
)

type subOut struct {
	data []byte
	err  error
}

type createOrderResponse struct {
	order *Order
	err   error
}

type createOrderRequest struct {
	cb    chan<- *createOrderResponse
	order *Order
}

type APIClient struct {
	apiKey string

	createOrderChan chan *createOrderRequest
}

const (
	TRADIAS_WEBSOCKET_CHANNEL        = "wss://otcapp-uat.tradias.de/otc/ws"
	TRADIAS_SUBSCRIPTION_TYPE        = "subscribe"
	TRADIAS_SUBSCRIPTION_TYPE_STATUS = "subscribed"
	TRADIAS_HEARTBEAT_EVENT          = "heartbeat"
)

func NewClient(apiKey string) *APIClient {
	c := &APIClient{
		apiKey: apiKey,
	}

	return c
}

type subscribeMessage struct {
	Type        string `json:"type"`
	ChannelName string `json:"channelname"`
	Instrument  string `json:"instrument,omitempty"`
	Heartbeat   bool   `json:"heartbeat"`
}

type websocketResponse struct {
	Type                  string          `json:"type"`
	Event                 *string         `json:"event"`
	Levels                *TickerResponse `json:"levels"`
	Order                 *Order          `json:"order"`
	Message               *string         `json:"message"`
	ClientOrderId         *string         `json:"client_order_id"`
	InternalClientOrderId *string         `json:"client_order_id_1"`
}

type websocketRequest struct {
	Type  string `json:"type"`
	Order *Order `json:"order"`
}

func (c *APIClient) connect(ctx context.Context, msg *subscribeMessage) (*websocket.Conn, error) {
	header := map[string][]string{"x-token-id": {c.apiKey}}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, TRADIAS_WEBSOCKET_CHANNEL, header)
	if err != nil {
		return nil, err
	}

	// sum up any errors
	err = func() error {
		err := conn.WriteJSON(msg)
		if err != nil {
			return err
		}

		return getSubscriptionResult(conn)
	}()
	if err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

func getSubscriptionResult(conn *websocket.Conn) error {
	for {
		fmt.Println("Receiving data from websocket...")
		_, data, err := conn.ReadMessage()
		if err == io.EOF {
			return err
		}
		if err != nil {
			fmt.Printf("error during websocket connection: %v\n", err)
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

func (c *APIClient) StartOrderChannel(ctx context.Context) error {
	sub := subscribeMessage{
		Type:        TRADIAS_SUBSCRIPTION_TYPE,
		ChannelName: ORDERS_CHANNEL_NAME,
	}

	conn, err := c.connect(ctx, &sub)
	if err != nil {
		return err
	}

	msgChan := make(chan *subOut, 1)
	c.createOrderChan = make(chan *createOrderRequest, 1)

	go handleOrderRequests(ctx, conn, c.createOrderChan, msgChan)
	go listenToSocket(conn, msgChan)

	return nil
}

func handleOrderRequests(ctx context.Context, conn *websocket.Conn, createOrderChan <-chan *createOrderRequest, msgReceived <-chan *subOut) {
	defer conn.Close()

	// create timeout
	requests := make(map[string]*createOrderRequest)

	// drain receive channel
	defer func() {
		for msg := <-msgReceived; msg != nil; msg = <-msgReceived {
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-msgReceived:
			if msg == nil {
				return
			}

			if msg.err != nil {
				fmt.Printf("received websocket error: shutting down order channel: %v\n", msg.err)
				return
			}

			fmt.Printf("MSG: %v\n", string(msg.data))
			wsMsg := new(websocketResponse)
			err := json.Unmarshal(msg.data, wsMsg)
			if err != nil {
				fmt.Printf("error unmarshaling websocket message: shutting down order channel: %v\n", err)
				return
			}

			if wsMsg.Message != nil {
				if wsMsg.Message == nil ||
					wsMsg.InternalClientOrderId == nil {
					fmt.Println("error websocket error: shutting down order channel", err)
					return
				}

				// TODO send open failed to all listening

				if req, ok := requests[*wsMsg.InternalClientOrderId]; ok {
					req.cb <- &createOrderResponse{err: fmt.Errorf("order failed to open: %v", *wsMsg.Message)}
					close(req.cb)
					delete(requests, *wsMsg.InternalClientOrderId)
				}

				continue
			}

			if wsMsg.Event == nil {
				continue
			}

			if *wsMsg.Event != ORDER_EVENT {
				continue
			}

			if wsMsg.Order == nil {
				fmt.Println("error processing order event. no order found: shutting down order channel")
			}

			// TODO insert all getting ws here

			if req, ok := requests[wsMsg.Order.InternalClientOrderId]; ok {
				req.cb <- &createOrderResponse{order: wsMsg.Order}
				close(req.cb)
				delete(requests, wsMsg.Order.InternalClientOrderId)
			}

		case req := <-createOrderChan:
			if req == nil {
				fmt.Println("create order channel closed")
				return
			}

			msg := websocketRequest{
				Type:  CREATE_ORDER_TYPE,
				Order: req.order,
			}

			requests[req.order.InternalClientOrderId] = req
			err := conn.WriteJSON(&msg)
			if err != nil {
				delete(requests, req.order.InternalClientOrderId)
				req.cb <- &createOrderResponse{err: err}
				close(req.cb)
				continue
			}
		}
	}
}

func listenToSocket(conn *websocket.Conn, sendMsg chan<- *subOut) {
	defer close(sendMsg)

	for {
		_, data, err := conn.ReadMessage()
		sendMsg <- &subOut{data: data, err: err}
	}
}
