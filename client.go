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

type allOrderSubscription struct {
	cb  chan<- *Order
	ctx context.Context
}

type APIClient struct {
	apiUrl string
	apiKey string

	createOrderChan        chan *createOrderRequest
	subscribeAllOrdersChan chan *allOrderSubscription
}

const (
	TRADIAS_WEBSOCKET_CHANNEL         = "wss://otcapp.tradias.de/otc/ws"
	TRADIAS_WEBSOCKET_CHANNEL_SANDBOX = "wss://otcapp-sandbox.tradias.de/otc/ws"
	TRADIAS_SUBSCRIPTION_TYPE         = "subscribe"
	TRADIAS_SUBSCRIPTION_TYPE_STATUS  = "subscribed"
	TRADIAS_HEARTBEAT_EVENT           = "heartbeat"
)

func NewClient(apiKey string) *APIClient {
	c := &APIClient{
		apiUrl: TRADIAS_WEBSOCKET_CHANNEL,
		apiKey: apiKey,
	}

	return c
}

func (c *APIClient) Sandbox() {
	c.apiUrl = TRADIAS_WEBSOCKET_CHANNEL_SANDBOX
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
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.apiUrl, header)
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
	c.subscribeAllOrdersChan = make(chan *allOrderSubscription, 1)

	go handleOrderRequests(ctx, conn, c.createOrderChan, c.subscribeAllOrdersChan, msgChan)
	go listenToSocket(conn, msgChan)

	return nil
}

func (c *APIClient) SubscribeAllOrders(ctx context.Context) (<-chan *Order, error) {
	result := make(chan *Order, 1)

	sub := &allOrderSubscription{
		ctx: ctx,
		cb:  result,
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case c.subscribeAllOrdersChan <- sub:
	}

	return result, nil
}

func handleOrderRequests(ctx context.Context, conn *websocket.Conn, createOrderChan <-chan *createOrderRequest, subscriptionChan <-chan *allOrderSubscription, msgReceived <-chan *subOut) {
	defer conn.Close()

	// create timeout
	requests := make(map[string]*createOrderRequest)

	subscriptions := make([]*allOrderSubscription, 0)
	defer func() {
		for _, sub := range subscriptions {
			close(sub.cb)
		}
	}()

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

			order := new(Order)
			err = json.Unmarshal(msg.data, order)
			if err != nil {
				fmt.Printf("error unmarshaling order: %v. shutting down order channel", err)
				return
			}

			toRemove := make([]int, 0)
			for i, sub := range subscriptions {
				select {
				case <-sub.ctx.Done():
					toRemove = append(toRemove, i)
					close(sub.cb)
				case sub.cb <- order:
				}
			}

			// remove subscriptions
			for _, idx := range toRemove {
				subscriptions[idx] = subscriptions[len(subscriptions)-1]
				subscriptions = subscriptions[:len(subscriptions)-1]
			}

			if req, ok := requests[order.InternalClientOrderId]; ok {
				req.cb <- &createOrderResponse{order: order}
				close(req.cb)
				delete(requests, order.InternalClientOrderId)
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
		case sub := <-subscriptionChan:
			if sub == nil {
				fmt.Println("subscription channel closed")
				return
			}

			subscriptions = append(subscriptions, sub)
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
