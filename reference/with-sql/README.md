# SQL 写入与事务发件箱

这个独立模块使用 SQLite 和 `database/sql` 演示同一服务的迁移、CRUD、幂等写入、并发更新和事件投递。SQLite 驱动依赖位于本模块。

## 运行

```sh
cd reference/with-sql
go run . -migrate -db items.db
read -rs SQL_API_TOKEN
export SQL_API_TOKEN
go run . -db items.db
```

默认监听 `127.0.0.1:8090`。使用 `GET /healthz` 检查进程，使用 `GET /readyz` 检查数据库。资源接口要求 `Authorization: Bearer <SQL_API_TOKEN>`。可以使用下列请求创建记录。

```sh
curl -fsS -i http://127.0.0.1:8090/items \
  -H "Authorization: Bearer $SQL_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-create-1' \
  -d '{"name":"widget"}'
```

- `POST /items` 返回 201、`Location` 和 `ETag`。24 小时内，同键同内容重放原始 201。同键异内容返回 409。
- `GET /items/:id` 返回当前记录和版本标签。
- `PUT /items/:id` 和 `DELETE /items/:id` 要求一个强 `If-Match` 标签或 `*`。标签过期或记录已消失时返回 412。更新和版本判定在同一 SQL 语句中完成。
- 创建时将资源、重放收据和待发送事件一起提交。worker 每轮处理最多 16 条事件，并为整轮设置 5 秒时限。投递失败后，下轮沿用同一个事件 ID 重试。
- 本地消费者以事件 ID 为主键，将投递结果写入 `delivered_events`。进程在写入后、删除发件箱记录前退出时，重启会补做确认，并保留原来的投递结果。接入其他消费者时，也需要为它实现持久去重。

## 分进程运行

默认同一进程运行 HTTP API 和 worker。也可在两个终端使用同一个数据库文件运行两个进程。

```sh
go run . -migrate -db items.db
go run . -mode api -db items.db
# 在另一个终端运行，worker 无需 SQL_API_TOKEN
go run . -mode worker -db items.db
```

更新已有示例数据库时，先停止进程并重新执行迁移。迁移会保留旧版资源和待投递事件。两个进程分别持有最多一个数据库连接。此示例按一个 API 进程和一个 worker 进程运行。
