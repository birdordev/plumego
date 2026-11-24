package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func signalQueue(wake chan<- struct{}) {
	select {
	case wake <- struct{}{}:
	default:
	}
}

func listenQueueNotifications(ctx context.Context, cfg *pgx.ConnConfig, wake chan<- struct{}) {
	for ctx.Err() == nil {
		listenQueueConnection(ctx, cfg, wake)
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func listenQueueConnection(ctx context.Context, cfg *pgx.ConnConfig, wake chan<- struct{}) {
	connectCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(connectCtx, cfg.Copy())
	if err != nil {
		return
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	var channel string
	if err := conn.QueryRow(connectCtx, "SELECT 'plumego_mq_' || md5(current_schema())").Scan(&channel); err != nil {
		return
	}
	if _, err := conn.Exec(connectCtx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return
	}
	cancel()
	// 接通或重连后主动探测，补偿订阅前提交的任务。
	signalQueue(wake)
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return
		}
		signalQueue(wake)
	}
}
