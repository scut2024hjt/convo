package redis

import (
	"context"
	"crypto/tls"
	"net"
	"sync"

	goredis "github.com/go-redis/redis"
)

// The legacy Redis client does not apply context cancellation to socket IO.
// Projection calls own their connections so cancellation never closes a
// connection being used by the vote/session client.
func newProjectionClient(ctx context.Context) (*goredis.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := *client.Options()
	options.PoolSize = 1
	options.MinIdleConns = 0
	options.MaxRetries = 0
	options.IdleCheckFrequency = -1
	options.Dialer = func() (net.Conn, error) {
		dialer := &net.Dialer{Timeout: options.DialTimeout}
		var connection net.Conn
		var err error
		if options.TLSConfig != nil {
			tlsDialer := &tls.Dialer{NetDialer: dialer, Config: options.TLSConfig}
			connection, err = tlsDialer.DialContext(ctx, options.Network, options.Addr)
		} else {
			connection, err = dialer.DialContext(ctx, options.Network, options.Addr)
		}
		if err != nil {
			return nil, err
		}
		owned := &projectionConnection{Conn: connection, done: make(chan struct{})}
		go func() {
			select {
			case <-ctx.Done():
				_ = owned.Close()
			case <-owned.done:
			}
		}()
		return owned, nil
	}
	return goredis.NewClient(&options), nil
}

type projectionConnection struct {
	net.Conn
	done chan struct{}
	once sync.Once
}

func (connection *projectionConnection) Close() error {
	connection.once.Do(func() { close(connection.done) })
	return connection.Conn.Close()
}
