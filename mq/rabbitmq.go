package mq

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/scut2024hjt/convo/models"
	"github.com/scut2024hjt/convo/settings"
)

const (
	RetryExchange = "convo.retry"
	RetryQueue    = "convo.vote.persist.retry.v1"
	DeadExchange  = "convo.dlx"
	DeadQueue     = "convo.vote.persist.dlq.v1"
)

type Client struct {
	config    *settings.RabbitMQConfig
	mu        sync.Mutex
	conn      *amqp.Connection
	publisher *amqp.Channel
	confirms  <-chan amqp.Confirmation
	returns   <-chan amqp.Return
}

func New(config *settings.RabbitMQConfig) (*Client, error) {
	client := &Client{config: config}
	client.mu.Lock()
	err := client.connectLocked()
	client.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || c.conn.IsClosed() {
		return nil
	}
	if c.publisher != nil {
		_ = c.publisher.Close()
	}
	return c.conn.Close()
}

func (c *Client) connectLocked() error {
	if c.conn != nil && !c.conn.IsClosed() {
		return nil
	}
	conn, err := amqp.Dial(c.config.URL)
	if err != nil {
		return err
	}
	if err = declareTopology(conn, c.config); err != nil {
		_ = conn.Close()
		return err
	}
	c.conn = conn
	c.publisher = nil
	c.confirms = nil
	c.returns = nil
	return nil
}

func (c *Client) publisherLocked() (*amqp.Channel, error) {
	if c.publisher != nil && !c.publisher.IsClosed() {
		return c.publisher, nil
	}
	channel, err := c.conn.Channel()
	if err != nil {
		_ = c.conn.Close()
		return nil, err
	}
	if err = channel.Confirm(false); err != nil {
		_ = channel.Close()
		return nil, err
	}
	c.publisher = channel
	c.confirms = channel.NotifyPublish(make(chan amqp.Confirmation, 1))
	c.returns = channel.NotifyReturn(make(chan amqp.Return, 1))
	return channel, nil
}

func (c *Client) invalidatePublisherLocked() {
	if c.publisher != nil {
		_ = c.publisher.Close()
	}
	c.publisher = nil
	c.confirms = nil
	c.returns = nil
}

func declareTopology(conn *amqp.Connection, config *settings.RabbitMQConfig) error {
	channel, err := conn.Channel()
	if err != nil {
		return err
	}
	defer channel.Close()

	for _, exchange := range []string{config.Exchange, RetryExchange, DeadExchange} {
		if err = channel.ExchangeDeclare(exchange, "direct", true, false, false, false, nil); err != nil {
			return err
		}
	}

	if _, err = channel.QueueDeclare(config.VoteQueue, true, false, false, false, nil); err != nil {
		return err
	}
	if err = channel.QueueBind(config.VoteQueue, models.VoteChangedEventType, config.Exchange, false, nil); err != nil {
		return err
	}

	retryArguments := amqp.Table{
		"x-message-ttl":             int32(config.RetryDelayMilliseconds),
		"x-dead-letter-exchange":    config.Exchange,
		"x-dead-letter-routing-key": models.VoteChangedEventType,
	}
	if _, err = channel.QueueDeclare(RetryQueue, true, false, false, false, retryArguments); err != nil {
		return err
	}
	if err = channel.QueueBind(RetryQueue, models.VoteChangedEventType, RetryExchange, false, nil); err != nil {
		return err
	}

	if _, err = channel.QueueDeclare(DeadQueue, true, false, false, false, nil); err != nil {
		return err
	}
	return channel.QueueBind(DeadQueue, models.VoteChangedEventType, DeadExchange, false, nil)
}

// Publish waits for both broker confirmation and mandatory routing feedback.
// Calls are serialized because confirms are scoped to an AMQP channel.
func (c *Client) Publish(ctx context.Context, exchange string, body []byte, messageID string, headers amqp.Table) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.connectLocked(); err != nil {
		return err
	}
	channel, err := c.publisherLocked()
	if err != nil {
		return err
	}

	publishCtx, cancel := context.WithTimeout(ctx, time.Duration(c.config.PublishConfirmTimeoutSeconds)*time.Second)
	defer cancel()
	err = channel.PublishWithContext(
		publishCtx,
		exchange,
		models.VoteChangedEventType,
		true,
		false,
		amqp.Publishing{
			Headers:      headers,
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    messageID,
			Type:         models.VoteChangedEventType,
			Timestamp:    time.Now(),
			Body:         body,
		},
	)
	if err != nil {
		c.invalidatePublisherLocked()
		return err
	}

	for {
		select {
		case returned := <-c.returns:
			c.invalidatePublisherLocked()
			return fmt.Errorf("rabbitmq returned message: %d %s", returned.ReplyCode, returned.ReplyText)
		case confirmation, ok := <-c.confirms:
			if !ok {
				c.invalidatePublisherLocked()
				return errors.New("rabbitmq confirm channel closed")
			}
			if !confirmation.Ack {
				c.invalidatePublisherLocked()
				return errors.New("rabbitmq rejected published message")
			}
			select {
			case returned := <-c.returns:
				c.invalidatePublisherLocked()
				return fmt.Errorf("rabbitmq returned message: %d %s", returned.ReplyCode, returned.ReplyText)
			default:
				return nil
			}
		case <-publishCtx.Done():
			c.invalidatePublisherLocked()
			return publishCtx.Err()
		}
	}
}

// ConsumeVote opens a manual-ack consumer channel. The caller must close the
// returned function and reopen the consumer when the deliveries channel closes.
func (c *Client) ConsumeVote(ctx context.Context, consumerName string) (<-chan amqp.Delivery, func() error, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.connectLocked(); err != nil {
		return nil, nil, err
	}
	channel, err := c.conn.Channel()
	if err != nil {
		_ = c.conn.Close()
		return nil, nil, err
	}
	if err = channel.Qos(c.config.ConsumerPrefetch, 0, false); err != nil {
		_ = channel.Close()
		return nil, nil, err
	}
	deliveries, err := channel.ConsumeWithContext(
		ctx,
		c.config.VoteQueue,
		consumerName,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		_ = channel.Close()
		return nil, nil, err
	}
	return deliveries, channel.Close, nil
}
