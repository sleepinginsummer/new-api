#!/usr/bin/env python3
# -*- coding: utf-8 -*-

"""
Veo 视频任务测试脚本。

用途：
1. 提交 Veo 视频生成任务到兼容 OpenAI Video API 的网关；
2. 轮询任务状态；
3. 任务完成后下载视频结果。

默认网关：
https://ai3.sleepinsum.com/
"""

import json
import os
import sys
import time
from pathlib import Path
from typing import Any, Dict, Optional
from urllib import error, request


DEFAULT_BASE_URL = "https://ai3.sleepinsum.com"
DEFAULT_MODEL = "veo-3.0-generate-001"
DEFAULT_PROMPT = "一只白色机械猫在雨夜霓虹街道行走，电影感镜头，16:9，动态追踪拍摄。"
DEFAULT_SIZE = "1280x720"
DEFAULT_DURATION = 8
DEFAULT_TIMEOUT_SECONDS = 600
DEFAULT_POLL_INTERVAL_SECONDS = 10


def build_headers(api_key: str) -> Dict[str, str]:
    """构建请求头。"""
    return {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {api_key}",
    }


def http_json(
    method: str,
    url: str,
    headers: Dict[str, str],
    payload: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    """发送 JSON 请求并返回解析后的 JSON 响应。"""
    data = None
    if payload is not None:
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")

    req = request.Request(url=url, data=data, headers=headers, method=method)
    try:
        with request.urlopen(req, timeout=120) as resp:
            body = resp.read().decode("utf-8")
            if not body.strip():
                return {}
            return json.loads(body)
    except error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(
            f"HTTP {exc.code} 请求失败\nURL: {url}\n响应体: {body}"
        ) from exc
    except error.URLError as exc:
        raise RuntimeError(f"网络请求失败: {exc}") from exc


def submit_veo_task(
    base_url: str,
    api_key: str,
    model: str,
    prompt: str,
    size: str,
    duration: int,
) -> str:
    """提交 Veo 视频生成任务并返回任务 ID。"""
    url = f"{base_url.rstrip('/')}/v1/videos"
    payload = {
        "model": model,
        "prompt": prompt,
        "size": size,
        "duration": duration,
    }
    response = http_json("POST", url, build_headers(api_key), payload)

    task_id = response.get("id") or response.get("task_id")
    if not task_id:
        raise RuntimeError(f"提交任务成功但未返回任务 ID: {response}")
    return task_id


def fetch_task(base_url: str, api_key: str, task_id: str) -> Dict[str, Any]:
    """查询任务状态。"""
    url = f"{base_url.rstrip('/')}/v1/videos/{task_id}"
    return http_json("GET", url, build_headers(api_key))


def resolve_status(task_payload: Dict[str, Any]) -> str:
    """从响应体中提取任务状态。"""
    status = str(task_payload.get("status", "")).lower()
    if status:
        return status

    data = task_payload.get("data")
    if isinstance(data, list) and data:
        item = data[0]
        if isinstance(item, dict):
            return str(item.get("status", "")).lower()
    return ""


def wait_until_done(
    base_url: str,
    api_key: str,
    task_id: str,
    timeout_seconds: int,
    poll_interval_seconds: int,
) -> Dict[str, Any]:
    """轮询任务直到成功、失败或超时。"""
    deadline = time.time() + timeout_seconds

    while time.time() < deadline:
        task_payload = fetch_task(base_url, api_key, task_id)
        status = resolve_status(task_payload)
        print(f"[轮询] task_id={task_id} status={status or 'unknown'}")
        print(json.dumps(task_payload, ensure_ascii=False, indent=2))

        if status in {"succeeded", "success", "completed"}:
            return task_payload
        if status in {"failed", "error", "cancelled", "canceled"}:
            raise RuntimeError(f"任务执行失败: {json.dumps(task_payload, ensure_ascii=False)}")

        time.sleep(poll_interval_seconds)

    raise TimeoutError(f"等待任务超时，task_id={task_id}")


def download_video(base_url: str, api_key: str, task_id: str, output_path: Path) -> Path:
    """下载视频结果到本地文件。"""
    url = f"{base_url.rstrip('/')}/v1/videos/{task_id}/content"
    req = request.Request(url=url, headers={"Authorization": f"Bearer {api_key}"}, method="GET")
    try:
        with request.urlopen(req, timeout=300) as resp:
            output_path.parent.mkdir(parents=True, exist_ok=True)
            output_path.write_bytes(resp.read())
    except error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(
            f"下载视频失败，HTTP {exc.code}\nURL: {url}\n响应体: {body}"
        ) from exc
    except error.URLError as exc:
        raise RuntimeError(f"下载视频网络异常: {exc}") from exc
    return output_path


def main() -> int:
    """脚本入口。"""
    api_key = os.getenv("NEW_API_KEY", "").strip()
    base_url = os.getenv("NEW_API_BASE_URL", DEFAULT_BASE_URL).strip()
    model = os.getenv("NEW_API_MODEL", DEFAULT_MODEL).strip()
    prompt = os.getenv("NEW_API_PROMPT", DEFAULT_PROMPT).strip()
    size = os.getenv("NEW_API_SIZE", DEFAULT_SIZE).strip()
    duration = int(os.getenv("NEW_API_DURATION", str(DEFAULT_DURATION)).strip())
    timeout_seconds = int(os.getenv("NEW_API_TIMEOUT", str(DEFAULT_TIMEOUT_SECONDS)).strip())
    poll_interval_seconds = int(
        os.getenv("NEW_API_POLL_INTERVAL", str(DEFAULT_POLL_INTERVAL_SECONDS)).strip()
    )
    output_path = Path(
        os.getenv("NEW_API_OUTPUT", f"/tmp/{model.replace('/', '_')}_{int(time.time())}.mp4").strip()
    )

    if not api_key:
        print("请先设置环境变量 NEW_API_KEY", file=sys.stderr)
        print("示例：export NEW_API_KEY='sk-xxxxx'", file=sys.stderr)
        return 1

    print("[提交任务]")
    print(f"base_url={base_url}")
    print(f"model={model}")
    print(f"size={size}")
    print(f"duration={duration}")
    print(f"output={output_path}")

    task_id = submit_veo_task(base_url, api_key, model, prompt, size, duration)
    print(f"[提交成功] task_id={task_id}")

    final_payload = wait_until_done(
        base_url=base_url,
        api_key=api_key,
        task_id=task_id,
        timeout_seconds=timeout_seconds,
        poll_interval_seconds=poll_interval_seconds,
    )
    print("[任务完成]")
    print(json.dumps(final_payload, ensure_ascii=False, indent=2))

    saved_path = download_video(base_url, api_key, task_id, output_path)
    print(f"[下载完成] {saved_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
