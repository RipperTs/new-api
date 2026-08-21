import React, { useEffect, useMemo, useState } from 'react';
import { VChart } from '@visactor/react-vchart';
import { initVChartSemiTheme } from '@visactor/vchart-semi-theme';
import { Modal, Spin, Tag, Typography } from '@douyinfe/semi-ui';
import { timestamp2string } from '../helpers';
import { renderNumber } from '../helpers/render';
import {
  AvailabilityPeriodSelector,
  formatAvailability,
  getAvailabilityColor,
} from './ChannelAvailability.js';

let chartThemeInitialized = false;

const MetricCard = ({ label, value, color }) => (
  <div
    style={{
      minWidth: 140,
      padding: '14px 16px',
      border: '1px solid var(--semi-color-border)',
      borderRadius: 10,
      background: 'var(--semi-color-fill-0)',
    }}
  >
    <Typography.Text type='tertiary' size='small'>
      {label}
    </Typography.Text>
    <div
      style={{
        marginTop: 5,
        color: color || 'var(--semi-color-text-0)',
        fontSize: 22,
        fontWeight: 700,
        fontVariantNumeric: 'tabular-nums',
      }}
    >
      {value}
    </div>
  </div>
);

const ChannelAvailabilityModal = ({
  visible,
  record,
  stat,
  period,
  loading,
  onPeriodChange,
  onCancel,
}) => {
  const [chartReady, setChartReady] = useState(chartThemeInitialized);

  useEffect(() => {
    if (visible && !chartThemeInitialized) {
      initVChartSemiTheme({ isWatchingThemeSwitch: true });
      chartThemeInitialized = true;
    }
    if (visible) {
      setChartReady(true);
    }
  }, [visible]);

  const availabilityColor = getAvailabilityColor(stat?.availability);
  const hasRequests = Boolean(stat && stat.request_count > 0);
  const successCount = hasRequests ? stat.request_count - stat.error_count : 0;

  const availabilityData = useMemo(
    () =>
      (stat?.trend || []).map((point) => ({
        Time: timestamp2string(point.bucket_time),
        Availability:
          point.availability === null || point.availability === undefined
            ? null
            : Number(point.availability),
        Requests: point.request_count,
        Errors: point.error_count,
      })),
    [stat],
  );

  const volumeData = useMemo(
    () =>
      (stat?.trend || []).flatMap((point) => [
        {
          Time: timestamp2string(point.bucket_time),
          Metric: '总请求',
          Count: point.request_count,
        },
        {
          Time: timestamp2string(point.bucket_time),
          Metric: '错误请求',
          Count: point.error_count,
        },
      ]),
    [stat],
  );

  const availabilitySpec = useMemo(
    () => ({
      type: 'line',
      data: [{ id: 'availability', values: availabilityData }],
      xField: 'Time',
      yField: 'Availability',
      point: { visible: true },
      line: {
        style: {
          stroke: availabilityColor,
          lineWidth: 3,
        },
      },
      title: {
        visible: true,
        text: '可用率趋势',
        subtext: '无请求的时间段不参与可用率计算',
      },
      tooltip: {
        mark: {
          content: [
            {
              key: '可用率',
              value: (datum) => `${datum.Availability.toFixed(2)}%`,
            },
            {
              key: '请求数',
              value: (datum) => renderNumber(datum.Requests),
            },
            {
              key: '错误数',
              value: (datum) => renderNumber(datum.Errors),
            },
          ],
        },
      },
    }),
    [availabilityColor, availabilityData],
  );

  const volumeSpec = useMemo(
    () => ({
      type: 'bar',
      data: [{ id: 'volume', values: volumeData }],
      xField: 'Time',
      yField: 'Count',
      seriesField: 'Metric',
      color: ['#2563eb', '#dc2626'],
      legends: { visible: true },
      title: {
        visible: true,
        text: '请求量与错误量',
        subtext: '按所选周期自动聚合',
      },
      tooltip: {
        mark: {
          content: [
            {
              key: (datum) => datum.Metric,
              value: (datum) => renderNumber(datum.Count),
            },
          ],
        },
      },
    }),
    [volumeData],
  );

  const modalTitle = (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span>{record?.name || '渠道可用性'}</span>
        {record?.id !== undefined && <Tag color='blue'>#{record.id}</Tag>}
      </div>
      <Typography.Text type='tertiary' size='small'>
        渠道调用可用性详情
      </Typography.Text>
    </div>
  );

  return (
    <Modal
      visible={visible}
      title={modalTitle}
      width={980}
      style={{ maxWidth: 'calc(100vw - 32px)' }}
      footer={null}
      centered
      onCancel={onCancel}
      bodyStyle={{
        maxHeight: 'calc(100vh - 170px)',
        overflowY: 'auto',
        padding: '18px 24px 24px',
      }}
    >
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: 12,
          marginBottom: 16,
          flexWrap: 'wrap',
        }}
      >
        <Typography.Text strong>统计周期</Typography.Text>
        <AvailabilityPeriodSelector value={period} onChange={onPeriodChange} />
      </div>
      <Spin spinning={loading}>
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))',
            gap: 12,
          }}
        >
          <MetricCard
            label='总请求'
            value={renderNumber(stat?.request_count || 0)}
          />
          <MetricCard
            label='成功请求'
            value={renderNumber(successCount)}
            color='#16a34a'
          />
          <MetricCard
            label='错误请求'
            value={renderNumber(stat?.error_count || 0)}
            color='#dc2626'
          />
          <MetricCard
            label='可用率'
            value={formatAvailability(stat?.availability)}
            color={availabilityColor}
          />
        </div>

        {hasRequests ? (
          chartReady ? (
            <>
              <div style={{ height: 330, marginTop: 18 }}>
                <VChart
                  spec={availabilitySpec}
                  option={{ mode: 'desktop-browser' }}
                />
              </div>
              <div style={{ height: 300, marginTop: 12 }}>
                <VChart
                  spec={volumeSpec}
                  option={{ mode: 'desktop-browser' }}
                />
              </div>
            </>
          ) : (
            <div style={{ padding: 56, textAlign: 'center' }}>图表加载中</div>
          )
        ) : (
          <div
            style={{
              marginTop: 18,
              padding: '56px 24px',
              border: '1px dashed var(--semi-color-border)',
              borderRadius: 10,
              color: 'var(--semi-color-text-2)',
              textAlign: 'center',
              background: 'var(--semi-color-fill-0)',
            }}
          >
            当前时间段暂无渠道调用数据
          </div>
        )}
      </Spin>
    </Modal>
  );
};

export default ChannelAvailabilityModal;
