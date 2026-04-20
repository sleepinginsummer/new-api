/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useEffect, useState } from 'react';
import {
  Button,
  Card,
  Input,
  Pagination,
  Space,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { API, showError } from '../../helpers';
import { useTranslation } from 'react-i18next';

const statusColorMap = {
  queued: 'orange',
  in_progress: 'blue',
  completed: 'green',
  error: 'red',
};

const statusLabelMap = {
  queued: '排队中',
  in_progress: '进行中',
  completed: '已完成',
  error: '错误',
};

const modeLabelMap = {
  sync: '同步',
  async: '异步',
};

function formatDuration(ms) {
  if (ms === null || ms === undefined || ms < 0) return '-';
  const totalSeconds = Math.floor(ms / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}

function formatTimestamp(timestamp) {
  if (!timestamp) return '-';
  return new Date(timestamp * 1000).toLocaleString();
}

const QueuePage = () => {
  const { t } = useTranslation();
  const [items, setItems] = useState([]);
  const [loading, setLoading] = useState(false);
  const [activePage, setActivePage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [total, setTotal] = useState(0);
  const [status, setStatus] = useState('');
  const [model, setModel] = useState('');
  const [channelId, setChannelId] = useState('');

  const loadData = async (page = activePage, size = pageSize) => {
    setLoading(true);
    try {
      const res = await API.get('/api/channel/queue/tasks', {
        params: {
          p: page,
          page_size: size,
          status,
          model,
          channel_id: channelId,
        },
      });
      const { success, data, message } = res.data;
      if (!success) {
        showError(message);
        return;
      }
      setItems(data.items || []);
      setTotal(data.total || 0);
      setActivePage(data.page || page);
      setPageSize(data.page_size || size);
    } catch (error) {
      showError(error);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData(1, pageSize);
    const timer = setInterval(() => {
      loadData(activePage, pageSize);
    }, 5000);
    return () => clearInterval(timer);
  }, [status, model, channelId, activePage, pageSize]);

  const columns = [
    {
      title: t('任务 ID'),
      dataIndex: 'task_id',
      render: (value) => (
        <Typography.Text copyable={{ content: value }}>{value}</Typography.Text>
      ),
    },
    {
      title: t('模型'),
      dataIndex: 'model',
    },
    {
      title: t('模式'),
      dataIndex: 'mode',
      render: (value) => (
        <Tag color={value === 'async' ? 'purple' : 'cyan'} shape='circle'>
          {t(modeLabelMap[value] || '同步')}
        </Tag>
      ),
    },
    {
      title: t('分组'),
      dataIndex: 'group',
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      render: (value) => (
        <Tag color={statusColorMap[value] || 'grey'} shape='circle'>
          {t(statusLabelMap[value] || value)}
        </Tag>
      ),
    },
    {
      title: t('当前处理渠道'),
      dataIndex: 'channel_name',
      render: (value, record) => value || (record.channel_id ? `${record.channel_id}` : '-'),
    },
    {
      title: t('进入队列时长'),
      dataIndex: 'queue_duration_ms',
      render: (value) => formatDuration(value),
    },
    {
      title: t('进行任务时长'),
      dataIndex: 'processing_duration_ms',
      render: (value) => formatDuration(value),
    },
    {
      title: t('重试次数'),
      dataIndex: 'retry_count',
    },
    {
      title: t('最近错误信息'),
      dataIndex: 'last_error',
      render: (value) => value || '-',
    },
    {
      title: t('创建时间'),
      dataIndex: 'created_at',
      render: (value) => formatTimestamp(value),
    },
    {
      title: t('最近状态变更时间'),
      dataIndex: 'updated_at',
      render: (value) => formatTimestamp(value),
    },
  ];

  return (
    <div className='mt-[60px] px-2'>
      <Card
        title={t('队列监控')}
        headerExtraContent={
          <Space wrap>
            <Input
              value={model}
              onChange={setModel}
              placeholder={t('模型')}
              style={{ width: 180 }}
            />
            <Input
              value={channelId}
              onChange={setChannelId}
              placeholder={t('渠道 ID')}
              style={{ width: 140 }}
            />
            <Input
              value={status}
              onChange={setStatus}
              placeholder={t('状态')}
              style={{ width: 140 }}
            />
            <Button onClick={() => loadData(1, pageSize)}>{t('刷新')}</Button>
          </Space>
        }
      >
        <Table
          rowKey='task_id'
          loading={loading}
          columns={columns}
          dataSource={items}
          pagination={false}
        />
        <div style={{ marginTop: 16, display: 'flex', justifyContent: 'flex-end' }}>
          <Pagination
            currentPage={activePage}
            pageSize={pageSize}
            total={total}
            onPageChange={(page) => loadData(page, pageSize)}
            onPageSizeChange={(size) => loadData(1, size)}
            showSizeChanger
          />
        </div>
      </Card>
    </div>
  );
};

export default QueuePage;
