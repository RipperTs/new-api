import React, { useContext, useEffect, useMemo, useRef, useState } from 'react';
import { initVChartSemiTheme } from '@visactor/vchart-semi-theme';
import { VChart } from '@visactor/react-vchart';
import {
  Button,
  Card,
  Col,
  Descriptions,
  Form,
  Layout,
  Row,
  Spin,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from '@douyinfe/semi-ui';
import {
  API,
  getTodayStartTimestamp,
  showError,
  timestamp2string,
} from '../../helpers';
import { renderNumber } from '../../helpers/render';
import { StyleContext } from '../../context/Style/index.js';

const formatSeconds = (milliseconds) => {
  const value = Number(milliseconds || 0) / 1000;
  return `${value.toFixed(2)} s`;
};

const formatRate = (rate) => `${Number(rate || 0).toFixed(2)}%`;

const getBucketSeconds = (startTimestamp, endTimestamp) => {
  if (!startTimestamp || !endTimestamp || endTimestamp <= startTimestamp) {
    return 3600;
  }
  const duration = endTimestamp - startTimestamp;
  if (duration <= 2 * 86400) {
    return 900;
  }
  if (duration <= 14 * 86400) {
    return 3600;
  }
  if (duration <= 90 * 86400) {
    return 86400;
  }
  return 7 * 86400;
};

const getBucketColor = (bucket) => {
  if (bucket.p95_frt <= 4000) {
    return '#65a30d';
  }
  if (bucket.p95_frt <= 10000) {
    return '#d97706';
  }
  return '#dc2626';
};

const getDefaultInputs = () => {
  const now = new Date();
  return {
    model_name: '',
    group: '',
    channel: '',
    start_timestamp: timestamp2string(getTodayStartTimestamp()),
    end_timestamp: timestamp2string(now.getTime() / 1000 + 3600),
  };
};

const ModelHealth = () => {
  const initialized = useRef(false);
  const [styleState] = useContext(StyleContext);
  const [loading, setLoading] = useState(false);
  const [groupOptions, setGroupOptions] = useState([]);
  const [inputs, setInputs] = useState(getDefaultInputs);
  const [healthData, setHealthData] = useState({
    summary: {},
    models: [],
    channels: [],
    trends: [],
  });

  const { model_name, group, channel, start_timestamp, end_timestamp } = inputs;

  const handleInputChange = (value, name) => {
    setInputs((inputs) => ({ ...inputs, [name]: value }));
  };

  const loadGroupOptions = async () => {
    try {
      const res = await API.get('/api/group/');
      const groups = res.data?.data || [];
      setGroupOptions(
        groups.map((group) => ({
          label: group,
          value: group,
        })),
      );
    } catch (e) {
      // ignore
    }
  };

  const loadHealthData = async () => {
    setLoading(true);
    try {
      const localStartTimestamp = Date.parse(start_timestamp) / 1000;
      const localEndTimestamp = Date.parse(end_timestamp) / 1000;
      if (!localStartTimestamp || Number.isNaN(localStartTimestamp)) {
        showError('起始时间无效');
        return;
      }
      if (!localEndTimestamp || Number.isNaN(localEndTimestamp)) {
        showError('结束时间无效');
        return;
      }
      if (localEndTimestamp <= localStartTimestamp) {
        showError('结束时间必须大于起始时间');
        return;
      }
      if (localEndTimestamp - localStartTimestamp > 90 * 86400) {
        showError('查询时间范围不能超过 90 天');
        return;
      }
      let url = `/api/log/model_health?model_name=${model_name}&group=${group}&channel=${channel}&start_timestamp=${localStartTimestamp}&end_timestamp=${localEndTimestamp}`;
      url = encodeURI(url);
      const res = await API.get(url);
      const { success, message, data } = res.data;
      if (success) {
        setHealthData({
          summary: data.summary || {},
          models: data.models || [],
          channels: data.channels || [],
          trends: data.trends || [],
        });
      } else {
        showError(message);
      }
    } catch (e) {
      showError('模型健康度数据加载失败');
    } finally {
      setLoading(false);
    }
  };

  const modelChartData = useMemo(() => {
    return healthData.models.slice(0, 12).flatMap((item) => [
      {
        Model: item.model_name,
        Metric: '平均首字',
        Time: Number((item.avg_frt / 1000).toFixed(3)),
      },
      {
        Model: item.model_name,
        Metric: 'P95首字',
        Time: Number((item.p95_frt / 1000).toFixed(3)),
      },
    ]);
  }, [healthData.models]);

  const slowRateData = useMemo(() => {
    return healthData.models.slice(0, 12).map((item) => ({
      Model: item.model_name,
      SlowRate: Number(item.slow_rate || 0),
    }));
  }, [healthData.models]);

  const trendData = useMemo(() => {
    return healthData.trends.map((item) => ({
      Time: timestamp2string(item.created_at),
      Model: item.model_name,
      FirstResponseTime: Number((item.p95_frt / 1000).toFixed(3)),
    }));
  }, [healthData.trends]);

  const stabilityRows = useMemo(() => {
    const trendMap = new Map();
    healthData.trends.forEach((item) => {
      if (!trendMap.has(item.model_name)) {
        trendMap.set(item.model_name, []);
      }
      trendMap.get(item.model_name).push(item);
    });

    return healthData.models.slice(0, 12).map((model) => ({
      model_name: model.model_name,
      buckets: (trendMap.get(model.model_name) || []).sort(
        (a, b) => a.created_at - b.created_at,
      ),
    }));
  }, [healthData.models, healthData.trends]);

  const modelChartSpec = useMemo(
    () => ({
      type: 'bar',
      data: [{ id: 'modelHealth', values: modelChartData }],
      xField: 'Model',
      yField: 'Time',
      seriesField: 'Metric',
      legends: {
        visible: true,
      },
      title: {
        visible: true,
        text: '模型首字用时对比',
        subtext: '单位：秒',
      },
      tooltip: {
        mark: {
          content: [
            {
              key: (datum) => datum.Metric,
              value: (datum) => `${datum.Time} s`,
            },
          ],
        },
      },
    }),
    [modelChartData],
  );

  const trendChartSpec = useMemo(
    () => ({
      type: 'line',
      data: [{ id: 'trend', values: trendData }],
      xField: 'Time',
      yField: 'FirstResponseTime',
      seriesField: 'Model',
      point: {
        visible: true,
      },
      legends: {
        visible: true,
        selectMode: 'single',
      },
      title: {
        visible: true,
        text: 'P95 首字用时趋势',
        subtext: '单位：秒',
      },
      tooltip: {
        mark: {
          content: [
            {
              key: (datum) => datum.Model,
              value: (datum) => `${datum.FirstResponseTime} s`,
            },
          ],
        },
      },
    }),
    [trendData],
  );

  const slowRateSpec = useMemo(
    () => ({
      type: 'bar',
      data: [{ id: 'slowRate', values: slowRateData }],
      xField: 'Model',
      yField: 'SlowRate',
      title: {
        visible: true,
        text: '慢首字请求占比',
        subtext: '首字用时 > 10 秒',
      },
      tooltip: {
        mark: {
          content: [
            {
              key: '慢请求占比',
              value: (datum) => `${datum.SlowRate.toFixed(2)}%`,
            },
          ],
        },
      },
    }),
    [slowRateData],
  );

  const columns = [
    {
      title: '模型',
      dataIndex: 'model_name',
      render: (text) => <Tag color='blue'>{text}</Tag>,
    },
    {
      title: '渠道',
      dataIndex: 'channel',
      render: (text, record) =>
        text
          ? `${text}${record.channel_name ? ` - ${record.channel_name}` : ''}`
          : '-',
    },
    {
      title: '请求数',
      dataIndex: 'count',
      render: (text) => renderNumber(text),
    },
    {
      title: '平均首字',
      dataIndex: 'avg_frt',
      render: (text) => formatSeconds(text),
    },
    {
      title: 'P50',
      dataIndex: 'p50_frt',
      render: (text) => formatSeconds(text),
    },
    {
      title: 'P90',
      dataIndex: 'p90_frt',
      render: (text) => formatSeconds(text),
    },
    {
      title: 'P95',
      dataIndex: 'p95_frt',
      render: (text) => formatSeconds(text),
    },
    {
      title: '最慢',
      dataIndex: 'max_frt',
      render: (text) => formatSeconds(text),
    },
    {
      title: '慢请求',
      dataIndex: 'slow_count',
      render: (text, record) =>
        `${renderNumber(text)} / ${formatRate(record.slow_rate)}`,
    },
  ];

  useEffect(() => {
    if (!initialized.current) {
      initVChartSemiTheme({
        isWatchingThemeSwitch: true,
      });
      initialized.current = true;
      loadGroupOptions().then();
      loadHealthData().then();
    }
  }, []);

  return (
    <Layout>
      <Form layout='horizontal' style={{ marginTop: 10 }}>
        <Form.Input
          field='model_name'
          label='模型名称'
          style={{ width: 176 }}
          value={model_name}
          placeholder='可选值'
          name='model_name'
          onChange={(value) => handleInputChange(value, 'model_name')}
        />
        {groupOptions.length > 0 ? (
          <Form.Select
            field='group'
            label='分组'
            style={{ width: 176 }}
            value={group}
            placeholder='全部分组'
            name='group'
            optionList={[{ label: '全部分组', value: '' }, ...groupOptions]}
            onChange={(value) => handleInputChange(value, 'group')}
          />
        ) : (
          <Form.Input
            field='group'
            label='分组'
            style={{ width: 176 }}
            value={group}
            placeholder='可选值'
            name='group'
            onChange={(value) => handleInputChange(value, 'group')}
          />
        )}
        <Form.Input
          field='channel'
          label='渠道 ID'
          style={{ width: 176 }}
          value={channel}
          placeholder='可选值'
          name='channel'
          onChange={(value) => handleInputChange(value, 'channel')}
        />
        <Form.DatePicker
          field='start_timestamp'
          label='起始时间'
          style={{ width: 272 }}
          initValue={start_timestamp}
          value={start_timestamp}
          type='dateTime'
          name='start_timestamp'
          onChange={(value) => handleInputChange(value, 'start_timestamp')}
        />
        <Form.DatePicker
          field='end_timestamp'
          label='结束时间'
          style={{ width: 272 }}
          initValue={end_timestamp}
          value={end_timestamp}
          type='dateTime'
          name='end_timestamp'
          onChange={(value) => handleInputChange(value, 'end_timestamp')}
        />
        <Button
          type='primary'
          onClick={loadHealthData}
          loading={loading}
          style={{ marginTop: 24 }}
        >
          查询
        </Button>
      </Form>
      <Spin spinning={loading}>
        <Row
          gutter={{ xs: 16, sm: 16, md: 16, lg: 24, xl: 24, xxl: 24 }}
          style={{ marginTop: 20 }}
          type='flex'
          justify='space-between'
        >
          <Col span={styleState.isMobile ? 24 : 6}>
            <Card>
              <Descriptions row size='small'>
                <Descriptions.Item itemKey='有效请求'>
                  {renderNumber(healthData.summary.count || 0)}
                </Descriptions.Item>
              </Descriptions>
            </Card>
          </Col>
          <Col span={styleState.isMobile ? 24 : 6}>
            <Card>
              <Descriptions row size='small'>
                <Descriptions.Item itemKey='平均首字'>
                  {formatSeconds(healthData.summary.avg_frt)}
                </Descriptions.Item>
              </Descriptions>
            </Card>
          </Col>
          <Col span={styleState.isMobile ? 24 : 6}>
            <Card>
              <Descriptions row size='small'>
                <Descriptions.Item itemKey='P95首字'>
                  {formatSeconds(healthData.summary.p95_frt)}
                </Descriptions.Item>
              </Descriptions>
            </Card>
          </Col>
          <Col span={styleState.isMobile ? 24 : 6}>
            <Card>
              <Descriptions row size='small'>
                <Descriptions.Item itemKey='慢请求占比'>
                  {formatRate(healthData.summary.slow_rate)}
                </Descriptions.Item>
              </Descriptions>
            </Card>
          </Col>
        </Row>
        <Card style={{ marginTop: 20 }}>
          <Tabs type='line' defaultActiveKey='1'>
            <Tabs.TabPane tab='稳定性' itemKey='1'>
              <div style={{ minHeight: 360, padding: '8px 0' }}>
                {stabilityRows.length === 0 ? (
                  <div style={{ color: '#8c8c8c', padding: 24 }}>
                    暂无可展示的首字稳定性数据
                  </div>
                ) : (
                  stabilityRows.map((row) => (
                    <div
                      key={row.model_name}
                      style={{
                        display: 'grid',
                        gridTemplateColumns: styleState.isMobile
                          ? '1fr'
                          : '132px minmax(0, 1fr)',
                        gap: 10,
                        alignItems: 'center',
                        marginBottom: 10,
                      }}
                    >
                      <Tag
                        color='blue'
                        style={{
                          width: styleState.isMobile ? 'auto' : 120,
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {row.model_name}
                      </Tag>
                      <div
                        style={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 2,
                          minWidth: 0,
                          width: '100%',
                          padding: '4px 0',
                        }}
                      >
                        {row.buckets.length === 0 ? (
                          <span style={{ color: '#8c8c8c', fontSize: 12 }}>
                            暂无调用数据
                          </span>
                        ) : (
                          row.buckets.map((bucket) => {
                            const tooltipContent = (
                              <div>
                                <div>
                                  时间：{timestamp2string(bucket.created_at)}
                                </div>
                                <div>请求数：{renderNumber(bucket.count)}</div>
                                <div>
                                  平均首字：{formatSeconds(bucket.avg_frt)}
                                </div>
                                <div>
                                  P95首字：{formatSeconds(bucket.p95_frt)}
                                </div>
                                <div>
                                  最慢首字：{formatSeconds(bucket.max_frt)}
                                </div>
                                <div>
                                  慢请求占比：{formatRate(bucket.slow_rate)}
                                </div>
                              </div>
                            );
                            return (
                              <Tooltip
                                key={bucket.created_at}
                                content={tooltipContent}
                              >
                                <div
                                  style={{
                                    minWidth: 2,
                                    maxWidth: 6,
                                    height: 28,
                                    borderRadius: 1,
                                    flex: '1 1 0',
                                    backgroundColor: getBucketColor(bucket),
                                  }}
                                />
                              </Tooltip>
                            );
                          })
                        )}
                      </div>
                    </div>
                  ))
                )}
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 16,
                    marginTop: 12,
                    color: '#6b7280',
                    fontSize: 12,
                  }}
                >
                  <span>
                    <i
                      style={{
                        display: 'inline-block',
                        width: 10,
                        height: 10,
                        backgroundColor: '#65a30d',
                        marginRight: 6,
                      }}
                    />
                    {'P95 <= 4s'}
                  </span>
                  <span>
                    <i
                      style={{
                        display: 'inline-block',
                        width: 10,
                        height: 10,
                        backgroundColor: '#d97706',
                        marginRight: 6,
                      }}
                    />
                    {'4s < P95 <= 10s'}
                  </span>
                  <span>
                    <i
                      style={{
                        display: 'inline-block',
                        width: 10,
                        height: 10,
                        backgroundColor: '#dc2626',
                        marginRight: 6,
                      }}
                    />
                    {'P95 > 10s'}
                  </span>
                </div>
              </div>
            </Tabs.TabPane>
            <Tabs.TabPane tab='首字对比' itemKey='2'>
              <div style={{ height: 460 }}>
                <VChart
                  spec={modelChartSpec}
                  option={{ mode: 'desktop-browser' }}
                />
              </div>
            </Tabs.TabPane>
            <Tabs.TabPane tab='趋势' itemKey='3'>
              <div style={{ height: 460 }}>
                <VChart
                  spec={trendChartSpec}
                  option={{ mode: 'desktop-browser' }}
                />
              </div>
            </Tabs.TabPane>
            <Tabs.TabPane tab='慢请求占比' itemKey='4'>
              <div style={{ height: 460 }}>
                <VChart
                  spec={slowRateSpec}
                  option={{ mode: 'desktop-browser' }}
                />
              </div>
            </Tabs.TabPane>
          </Tabs>
        </Card>
        <Card style={{ marginTop: 20 }}>
          <Table
            columns={columns}
            dataSource={healthData.channels}
            rowKey={(record) => `${record.model_name}-${record.channel}`}
            pagination={{
              pageSize: 10,
              pageSizeOpts: [10, 20, 50],
              showSizeChanger: true,
            }}
          />
        </Card>
      </Spin>
    </Layout>
  );
};

export default ModelHealth;
