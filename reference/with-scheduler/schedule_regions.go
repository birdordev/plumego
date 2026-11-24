package main

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"strconv"
)

const localSchedulerRegion = "local"

var errRegionVersion = errors.New("scheduler shard region or epoch changed")

type regionTransfer struct {
	Shard int
	Epoch int64
	From  string
	To    string
	Actor string
}

func (command regionTransfer) valid() bool {
	return command.Shard >= 0 && command.Shard < 64 && command.Epoch > 0 && command.Epoch < math.MaxInt64 &&
		runCursorID.MatchString(command.From) && runCursorID.MatchString(command.To) &&
		command.From != command.To && runCursorID.MatchString(command.Actor)
}

func (r repository) transferShardRegion(ctx context.Context, command regionTransfer) error {
	if !command.valid() {
		return errInvalidTask
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// 落锁顺序与心跳一致。分片行锁等待在途物化提交或回滚后才更换所有权。
	if _, err := tx.ExecContext(ctx, "SELECT singleton FROM scheduler_shard_control FOR UPDATE"); err != nil {
		return err
	}
	var from, to, actor string
	err = tx.QueryRowContext(ctx, `SELECT previous_region,next_region,actor FROM scheduler_region_transfers
		WHERE shard=$1 AND previous_epoch=$2`, command.Shard, command.Epoch).Scan(&from, &to, &actor)
	switch {
	case err == nil:
		if from != command.From || to != command.To || actor != command.Actor {
			return errRegionVersion
		}
		return tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE scheduler_shards SET region=$1, region_epoch=region_epoch+1, owner_id=NULL
		WHERE shard=$2 AND region=$3 AND region_epoch=$4`, command.To, command.Shard, command.From, command.Epoch)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errRegionVersion
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_region_transfers
		(shard,previous_epoch,previous_region,next_region,actor) VALUES ($1,$2,$3,$4,$5)`,
		command.Shard, command.Epoch, command.From, command.To, command.Actor); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE scheduler_shard_control SET transfers_pending=true"); err != nil {
		return err
	}
	return tx.Commit()
}

func loadRegionTransfer() (regionTransfer, error) {
	shard, shardErr := strconv.Atoi(os.Getenv("SCHEDULER_TRANSFER_SHARD"))
	epoch, epochErr := strconv.ParseInt(os.Getenv("SCHEDULER_TRANSFER_EPOCH"), 10, 64)
	command := regionTransfer{
		Shard: shard,
		Epoch: epoch,
		From:  os.Getenv("SCHEDULER_TRANSFER_FROM_REGION"),
		To:    os.Getenv("SCHEDULER_TRANSFER_TO_REGION"),
		Actor: os.Getenv("SCHEDULER_TRANSFER_ACTOR"),
	}
	if shardErr != nil || epochErr != nil || !command.valid() {
		return regionTransfer{}, errors.New("invalid scheduler region transfer configuration")
	}
	return command, nil
}
