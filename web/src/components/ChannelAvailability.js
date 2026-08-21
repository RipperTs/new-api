import React from 'react';
import { RadioGroup } from '@douyinfe/semi-ui';

const PERIOD_OPTIONS = [
  { label: '24 小时', value: '24h' },
  { label: '今天', value: 'today' },
  { label: '7 天', value: '7d' },
];

export const getAvailabilityColor = (availability) => {
  if (availability === null || availability === undefined) {
    return '#94a3b8';
  }
  if (availability >= 90) {
    return '#16a34a';
  }
  if (availability >= 70) {
    return '#ca8a04';
  }
  if (availability >= 50) {
    return '#ea580c';
  }
  return '#dc2626';
};

export const formatAvailability = (availability) => {
  if (availability === null || availability === undefined) {
    return '暂无数据';
  }
  return `${Number(availability).toFixed(2)}%`;
};

const buildSparklineSegments = (trend, width, height) => {
  const values = trend
    .map((point) => point.availability)
    .filter((value) => value !== null && value !== undefined);
  if (values.length === 0) {
    return [];
  }

  const minimum = Math.min(...values);
  const lowerBound = Math.max(
    0,
    minimum - Math.max(0.2, (100 - minimum) * 0.15),
  );
  const range = Math.max(0.1, 100 - lowerBound);
  const denominator = Math.max(1, trend.length - 1);
  const segments = [];
  let current = [];

  trend.forEach((point, index) => {
    if (point.availability === null || point.availability === undefined) {
      if (current.length > 0) {
        segments.push(current);
        current = [];
      }
      return;
    }
    const x = (index / denominator) * width;
    const y = 2 + ((100 - point.availability) / range) * (height - 4);
    current.push(`${x.toFixed(2)},${y.toFixed(2)}`);
  });
  if (current.length > 0) {
    segments.push(current);
  }
  return segments;
};

const AvailabilitySparkline = ({ stat }) => {
  const width = 146;
  const height = 34;
  const trend = stat?.trend || [];
  const segments = buildSparklineSegments(trend, width, height);
  const color = getAvailabilityColor(stat?.availability);

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      role='img'
      aria-label='渠道可用率趋势'
      style={{ flexShrink: 0 }}
    >
      <line
        x1='0'
        y1={height - 1}
        x2={width}
        y2={height - 1}
        stroke='var(--semi-color-border)'
        strokeWidth='1'
      />
      {segments.length === 0 ? (
        <line
          x1='0'
          y1={height / 2}
          x2={width}
          y2={height / 2}
          stroke='#94a3b8'
          strokeWidth='1.5'
          strokeDasharray='4 4'
        />
      ) : (
        segments.map((points, index) => {
          if (points.length === 1) {
            const [cx, cy] = points[0].split(',');
            return (
              <circle
                key={`${points[0]}-${index}`}
                cx={cx}
                cy={cy}
                r='2.5'
                fill={color}
              />
            );
          }
          return (
            <polyline
              key={`${points[0]}-${index}`}
              points={points.join(' ')}
              fill='none'
              stroke={color}
              strokeWidth='2.25'
              strokeLinecap='round'
              strokeLinejoin='round'
            />
          );
        })
      )}
    </svg>
  );
};

export const AvailabilityPeriodSelector = ({ value, onChange }) => (
  <RadioGroup
    type='button'
    buttonSize='small'
    value={value}
    options={PERIOD_OPTIONS}
    aria-label='可用性统计周期'
    onChange={(event) => onChange(event.target.value)}
  />
);

export const ChannelAvailabilityCell = ({ stat, loading, onClick }) => {
  const hasAvailability =
    stat?.availability !== null && stat?.availability !== undefined;
  const color = hasAvailability
    ? getAvailabilityColor(stat.availability)
    : 'var(--semi-color-text-2)';

  return (
    <button
      type='button'
      onClick={onClick}
      aria-label='查看渠道可用性详情'
      style={{
        width: 238,
        display: 'grid',
        gridTemplateColumns: '64px 146px',
        alignItems: 'center',
        columnGap: 8,
        padding: '6px 8px',
        border: '1px solid var(--semi-color-border)',
        borderRadius: 8,
        color: 'var(--semi-color-text-0)',
        background: 'var(--semi-color-fill-0)',
        cursor: 'pointer',
        font: 'inherit',
        opacity: loading ? 0.58 : 1,
        transition:
          'border-color 160ms ease, background 160ms ease, opacity 160ms ease',
      }}
    >
      <span
        style={{
          display: 'block',
          color,
          fontSize: hasAvailability ? 14 : 12,
          fontWeight: hasAvailability ? 600 : 400,
          fontVariantNumeric: 'tabular-nums',
          textAlign: 'left',
          whiteSpace: 'nowrap',
        }}
      >
        {loading && !stat ? '统计中' : formatAvailability(stat?.availability)}
      </span>
      <AvailabilitySparkline stat={stat} />
    </button>
  );
};
