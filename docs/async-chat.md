# Async Chat Completions API

## Overview

This project keeps the existing synchronous `POST /v1/chat/completions` behavior unchanged and adds an async variant for long-running Nanobanana image generation tasks.

The async API consists of:

- `POST /v1/chat/completions/async`
- `GET /v1/chat/completions/async/:uuid`

Async task metadata and final results are stored in Redis with a fixed TTL of `2h`.

## Submit Async Task

### Request

- Method: `POST`
- Path: `/v1/chat/completions/async`
- Auth: same Bearer token as `/v1/chat/completions`
- Request body: exactly the same as `/v1/chat/completions`

### Constraints

- `stream=true` is not supported for async mode
- Redis must be enabled, otherwise the API returns an error

### Success Response

```json
{
  "id": "7d54805f-4f42-4bdb-8b4b-8af6386173ea",
  "object": "chat.completion.async",
  "status": "processing",
  "created": 1710000000
}
```

### Error Response

```json
{
  "error": {
    "message": "stream is not supported for async chat completions",
    "type": "async_chat_error"
  }
}
```

## Poll Async Task

### Request

- Method: `GET`
- Path: `/v1/chat/completions/async/:uuid`
- Auth: same Bearer token as submission

### Processing Response

```json
{
  "id": "7d54805f-4f42-4bdb-8b4b-8af6386173ea",
  "object": "chat.completion.async",
  "status": "processing",
  "created": 1710000000,
  "updated": 1710000100
}
```

### Error Response

```json
{
  "id": "7d54805f-4f42-4bdb-8b4b-8af6386173ea",
  "object": "chat.completion.async",
  "status": "error",
  "created": 1710000000,
  "updated": 1710000100,
  "error": {
    "message": "upstream provider timeout",
    "type": "async_task_error"
  }
}
```

### Expired Or Missing Task

```json
{
  "id": "7d54805f-4f42-4bdb-8b4b-8af6386173ea",
  "object": "chat.completion.async",
  "status": "error",
  "error": {
    "message": "task expired or not found",
    "type": "async_task_expired"
  }
}
```

### Completed Response

When the task completes successfully, the polling endpoint returns the stored final JSON body of `/v1/chat/completions` directly and adds two headers:

- `X-Async-Task-Id`
- `X-Async-Task-Status: completed`

## Retention

- Redis result retention: `2h`
- Queue monitor retention for async tasks: `2h`
- Async task log retention: `2h`
