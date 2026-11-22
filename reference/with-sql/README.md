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
- 创建时将资源、重放收据和待发送事件一起提交。单个 worker 批量检查发件箱，投递失败会在下一轮重试并沿用同一个事件 ID。
- 示例的投递目标是本地日志。生产消费者需要按事件 ID 去重，因为进程有可能在投递成功后、删除发件箱记录前退出。
- 示例使用单进程和单连接。多副本部署需要根据数据库补充连接池与待发送记录的争用控制。
