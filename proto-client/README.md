# @chaitin-ai/agent-compose-client

agent-compose 的 ConnectRPC/protobuf TypeScript 客户端，由 `proto/` 生成。

## 安装

```bash
npm install @chaitin-ai/agent-compose-client
```

## 使用

```ts
import { createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { HealthService } from "@chaitin-ai/agent-compose-client/health/v1/health_connect.js";

const transport = createConnectTransport({ baseUrl: "/" });
const health = createPromiseClient(HealthService, transport);
```

## 维护

本包不提交生成代码；CI 在打 `client-v*` tag 时从 `proto/` 现生成并发布。
本地可运行 `npm install && npm run gen && npm run build` 验证。
