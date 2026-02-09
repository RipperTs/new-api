import React, { useContext, useEffect, useRef, useState } from 'react';
import { initVChartSemiTheme } from '@visactor/vchart-semi-theme';

import { Button, Card, Col, Descriptions, Form, Input, Layout, Modal, Row, Select, Spin, Switch, Tabs, Typography } from '@douyinfe/semi-ui';
import { VChart } from "@visactor/react-vchart";
import {
  API,
  isAdmin,
  showError,
  timestamp2string,
  timestamp2string1,
} from '../../helpers';
import {
  getQuotaWithUnit,
  modelColorMap,
  renderNumber,
  renderQuota,
  renderQuotaNumberWithDigit,
  stringToColor,
  modelToColor,
} from '../../helpers/render';
import { UserContext } from '../../context/User/index.js';
import { StyleContext } from '../../context/Style/index.js';

const MODEL_PRICE_STORAGE_KEY = 'detail_model_price_config_v1';
const MODEL_PRICE_MODE_STORAGE_KEY = 'detail_model_price_mode_v1';
const DEFAULT_INPUT_PRICE = 0;
const DEFAULT_OUTPUT_PRICE = 0;

const loadModelPriceConfig = () => {
  try {
    const raw = localStorage.getItem(MODEL_PRICE_STORAGE_KEY);
    if (!raw) {
      return {};
    }
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (e) {
    return {};
  }
};

const Detail = (props) => {
  const formRef = useRef();
  let now = new Date();
  const [userState, userDispatch] = useContext(UserContext);
  const [styleState, styleDispatch] = useContext(StyleContext);
  const [groupOptions, setGroupOptions] = useState([]);
  const [allModelOptions, setAllModelOptions] = useState([]);
  const [modelOptions, setModelOptions] = useState([]);
  const [inputs, setInputs] = useState({
    username: '',
    token_id: '',
    model_names: [],
    group: '',
    start_timestamp:
      localStorage.getItem('data_export_default_time') === 'hour'
        ? timestamp2string(now.getTime() / 1000 - 86400)
        : localStorage.getItem('data_export_default_time') === 'week'
          ? timestamp2string(now.getTime() / 1000 - 86400 * 30)
          : timestamp2string(now.getTime() / 1000 - 86400 * 7),
    end_timestamp: timestamp2string(now.getTime() / 1000 + 3600),
    channel: '',
    data_export_default_time: '',
  });
  const { username, model_names, start_timestamp, end_timestamp, channel, token_id } =
    inputs;
  const { group } = inputs;
  const isAdminUser = isAdmin();
  const initialized = useRef(false);
  const [loading, setLoading] = useState(false);
  const [quotaData, setQuotaData] = useState([]);
  const [consumeQuota, setConsumeQuota] = useState(0);
  const [consumeTokens, setConsumeTokens] = useState(0);
  const [times, setTimes] = useState(0);
  const [dataExportDefaultTime, setDataExportDefaultTime] = useState(
    localStorage.getItem('data_export_default_time') || 'hour',
  );
  const [pieData, setPieData] = useState([{ type: 'null', value: '0' }]);
  const [lineData, setLineData] = useState([]);
  const [spec_pie, setSpecPie] = useState({
    type: 'pie',
    data: [{
      id: 'id0',
      values: pieData
    }],
    outerRadius: 0.8,
    innerRadius: 0.5,
    padAngle: 0.6,
    valueField: 'value',
    categoryField: 'type',
    pie: {
      style: {
        cornerRadius: 10,
      },
      state: {
        hover: {
          outerRadius: 0.85,
          stroke: '#000',
          lineWidth: 1,
        },
        selected: {
          outerRadius: 0.85,
          stroke: '#000',
          lineWidth: 1,
        },
      },
    },
    title: {
      visible: true,
      text: '模型调用次数占比',
      subtext: `总计：${renderNumber(times)}`,
    },
    legends: {
      visible: true,
      orient: 'left',
    },
    label: {
      visible: true,
    },
    tooltip: {
      mark: {
        content: [
          {
            key: (datum) => datum['type'],
            value: (datum) => renderNumber(datum['value']),
          },
        ],
      },
    },
    color: {
      specified: modelColorMap,
    },
  });
  const [spec_line, setSpecLine] = useState({
    type: 'bar',
    data: [{
      id: 'barData',
      values: lineData
    }],
    xField: 'Time',
    yField: 'Usage',
    seriesField: 'Model',
    stack: true,
    legends: {
      visible: true,
      selectMode: 'single',
    },
    title: {
      visible: true,
      text: '模型消耗分布',
      subtext: `总计：${renderQuota(consumeQuota, 2)}`,
    },
    bar: {
      // The state style of bar
      state: {
        hover: {
          stroke: '#000',
          lineWidth: 1,
        },
      },
    },
    tooltip: {
      mark: {
        content: [
          {
            key: (datum) => datum['Model'],
            value: (datum) =>
              renderQuotaNumberWithDigit(parseFloat(datum['Usage']), 4),
          },
        ],
      },
      dimension: {
        content: [
          {
            key: (datum) => datum['Model'],
            value: (datum) => datum['Usage'],
          },
        ],
        updateContent: (array) => {
          // sort by value
          array.sort((a, b) => b.value - a.value);
          // add $
          let sum = 0;
          for (let i = 0; i < array.length; i++) {
            sum += parseFloat(array[i].value);
            array[i].value = renderQuotaNumberWithDigit(
              parseFloat(array[i].value),
              4,
            );
          }
          // add to first
          array.unshift({
            key: '总计',
            value: renderQuotaNumberWithDigit(sum, 4),
          });
          return array;
        },
      },
    },
    color: {
      specified: modelColorMap,
    },
  });

  // 添加一个新的状态来存储模型-颜色映射
  const [modelColors, setModelColors] = useState({});
  const [currentModels, setCurrentModels] = useState([]);
  const [modelPriceModalVisible, setModelPriceModalVisible] = useState(false);
  const [modelPriceConfig, setModelPriceConfig] = useState(() => loadModelPriceConfig());
  const [editingModelPrices, setEditingModelPrices] = useState({});
  const [customPriceEnabled, setCustomPriceEnabled] = useState(false);

  const handleInputChange = (value, name) => {
    if (name === 'data_export_default_time') {
      setDataExportDefaultTime(value);
      return;
    }
    setInputs((inputs) => ({ ...inputs, [name]: value }));
  };

  const renderCurrency = (value) => {
    return `$${Number(value || 0).toFixed(4)}`;
  };

  const getModelPrice = (modelName, priceConfig = modelPriceConfig) => {
    const config = priceConfig[modelName] || {};
    const inputPrice = Number(config.input_price);
    const outputPrice = Number(config.output_price);
    return {
      inputPrice: Number.isFinite(inputPrice) ? inputPrice : DEFAULT_INPUT_PRICE,
      outputPrice: Number.isFinite(outputPrice) ? outputPrice : DEFAULT_OUTPUT_PRICE,
    };
  };

  const calcUsageByItem = (item, priceConfig = modelPriceConfig, useCustomPrice = customPriceEnabled) => {
    if (!useCustomPrice) {
      return parseFloat(getQuotaWithUnit(item.quota));
    }
    const { inputPrice, outputPrice } = getModelPrice(item.model_name, priceConfig);
    const promptTokens = Number(item.prompt_tokens || 0);
    const completionTokens = Number(item.completion_tokens || 0);
    return (promptTokens / 1000000) * inputPrice + (completionTokens / 1000000) * outputPrice;
  };

  const openModelPriceModal = () => {
    const models = currentModels.filter((modelName) => modelName && modelName !== '无数据');
    const next = {};
    models.forEach((modelName) => {
      const config = modelPriceConfig[modelName] || {};
      next[modelName] = {
        input_price: config.input_price ?? '',
        output_price: config.output_price ?? '',
      };
    });
    setEditingModelPrices(next);
    setModelPriceModalVisible(true);
  };

  const updateEditingModelPrice = (modelName, field, value) => {
    setEditingModelPrices((prev) => ({
      ...prev,
      [modelName]: {
        ...(prev[modelName] || {}),
        [field]: value,
      },
    }));
  };

  const saveModelPriceConfig = async () => {
    const nextConfig = { ...modelPriceConfig };
    for (const modelName of Object.keys(editingModelPrices)) {
      const rawInput = editingModelPrices[modelName]?.input_price;
      const rawOutput = editingModelPrices[modelName]?.output_price;
      if ((rawInput === '' || rawInput === undefined || rawInput === null) && (rawOutput === '' || rawOutput === undefined || rawOutput === null)) {
        delete nextConfig[modelName];
        continue;
      }
      const inputPrice = Number(rawInput);
      const outputPrice = Number(rawOutput);
      if (!Number.isFinite(inputPrice) || inputPrice < 0 || !Number.isFinite(outputPrice) || outputPrice < 0) {
        showError(`${modelName} 的价格必须是大于等于 0 的数字`);
        return;
      }
      nextConfig[modelName] = {
        input_price: inputPrice,
        output_price: outputPrice,
      };
    }
    setModelPriceConfig(nextConfig);
    localStorage.setItem(MODEL_PRICE_STORAGE_KEY, JSON.stringify(nextConfig));
    setModelPriceModalVisible(false);
    await loadQuotaData(customPriceEnabled, nextConfig);
  };

  const handleCustomPriceSwitchChange = async (value) => {
    setCustomPriceEnabled(value);
    localStorage.setItem(MODEL_PRICE_MODE_STORAGE_KEY, value ? 'true' : 'false');
    if (!value) {
      setModelOptions(allModelOptions);
    }
    await loadQuotaData(value);
  };

  const loadGroupOptions = async () => {
    try {
      if (isAdminUser) {
        const res = await API.get(`/api/group/`);
        const groups = res.data?.data || [];
        setGroupOptions(
          groups.map((g) => ({
            label: g,
            value: g,
          })),
        );
        return;
      }
      const res = await API.get(`/api/user/self/groups`);
      const { success, data } = res.data;
      if (success) {
        const groups = Object.keys(data || {});
        setGroupOptions(
          groups.map((g) => ({
            label: g,
            value: g,
          })),
        );
      }
    } catch (e) {
      // ignore
    }
  };

  const updateModelOptions = (models = []) => {
    setModelOptions((prev) => {
      const optionMap = new Map(prev.map((item) => [item.value, item]));
      models.forEach((modelName) => {
        if (!modelName) {
          return;
        }
        if (!optionMap.has(modelName)) {
          optionMap.set(modelName, { label: modelName, value: modelName });
        }
      });
      return Array.from(optionMap.values()).sort((a, b) =>
        String(a.value).localeCompare(String(b.value)),
      );
    });
  };

  const loadModelOptions = async () => {
    try {
      const res = await API.get('/api/user/models');
      const { success, data } = res.data;
      if (success && Array.isArray(data)) {
        const options = data
          .filter((modelName) => !!modelName)
          .map((modelName) => ({
            label: modelName,
            value: modelName,
          }))
          .sort((a, b) => String(a.value).localeCompare(String(b.value)));
        setAllModelOptions(options);
        setModelOptions(options);
      }
    } catch (e) {
      // ignore
    }
  };

  const loadQuotaData = async (usePromptCompletionOverride = null, priceConfigOverride = null) => {
    setLoading(true);
    try {
      let url = '';
      let localStartTimestamp = Date.parse(start_timestamp) / 1000;
      let localEndTimestamp = Date.parse(end_timestamp) / 1000;
      const shouldUsePromptCompletion =
        usePromptCompletionOverride === null
          ? customPriceEnabled
          : usePromptCompletionOverride;
      const usePromptCompletion = shouldUsePromptCompletion ? 'true' : 'false';
      const modelNamesParam = encodeURIComponent((model_names || []).join(','));
      if (isAdminUser) {
        url = `/api/data/?username=${username}&group=${group}&token_id=${token_id}&model_names=${modelNamesParam}&use_prompt_completion=${usePromptCompletion}&start_timestamp=${localStartTimestamp}&end_timestamp=${localEndTimestamp}&default_time=${dataExportDefaultTime}`;
      } else {
        url = `/api/data/self/?group=${group}&token_id=${token_id}&model_names=${modelNamesParam}&use_prompt_completion=${usePromptCompletion}&start_timestamp=${localStartTimestamp}&end_timestamp=${localEndTimestamp}&default_time=${dataExportDefaultTime}`;
      }
      const res = await API.get(url);
      const { success, message, data } = res.data;
      if (success) {
        setQuotaData(data);
        if (data.length === 0) {
          data.push({
            count: 0,
            model_name: '无数据',
            quota: 0,
            created_at: now.getTime() / 1000,
          });
        }
        // 根据dataExportDefaultTime重制时间粒度
        let timeGranularity = 3600;
        if (dataExportDefaultTime === 'day') {
          timeGranularity = 86400;
        } else if (dataExportDefaultTime === 'week') {
          timeGranularity = 604800;
        }
        // sort created_at
        data.sort((a, b) => a.created_at - b.created_at);
        data.forEach((item) => {
          item['created_at'] =
            Math.floor(item['created_at'] / timeGranularity) * timeGranularity;
        });
        updateChartData(data, priceConfigOverride, shouldUsePromptCompletion);
      } else {
        showError(message);
      }
    } finally {
      setLoading(false);
    }
  };

  const refresh = async () => {
    await loadQuotaData();
  };

  const initChart = async () => {
    await loadQuotaData();
  };

  const updateChartData = (data, priceConfigOverride = null, useCustomPrice = customPriceEnabled) => {
    let newPieData = [];
    let newLineData = [];
    let totalQuota = 0;
    let totalTimes = 0;
    let uniqueModels = new Set();
    let totalTokens = 0;

    // 收集所有唯一的模型名称和时间点
    let uniqueTimes = new Set();
    data.forEach(item => {
      uniqueModels.add(item.model_name);
      uniqueTimes.add(timestamp2string1(item.created_at, dataExportDefaultTime));
      totalTokens += Number(item.token_used || 0);
    });
    const modelList = Array.from(uniqueModels);
    setCurrentModels(modelList);
    if (useCustomPrice) {
      const usedOptions = modelList
        .filter((modelName) => !!modelName && modelName !== '无数据')
        .map((modelName) => ({
          label: modelName,
          value: modelName,
        }))
        .sort((a, b) => String(a.value).localeCompare(String(b.value)));
      setModelOptions(usedOptions);
    } else {
      setModelOptions(allModelOptions);
    }

    // 处理颜色映射
    const newModelColors = {};
    modelList.forEach((modelName) => {
      newModelColors[modelName] = modelColorMap[modelName] ||
        modelColors[modelName] ||
        modelToColor(modelName);
    });
    setModelColors(newModelColors);

    // 处理饼图数据
    for (let item of data) {
      if (useCustomPrice) {
        totalQuota += calcUsageByItem(item, priceConfigOverride || modelPriceConfig, useCustomPrice);
      } else {
        totalQuota += item.quota;
      }
      totalTimes += item.count;

      let pieItem = newPieData.find((it) => it.type === item.model_name);
      if (pieItem) {
        pieItem.value += item.count;
      } else {
        newPieData.push({
          type: item.model_name,
          value: item.count,
        });
      }
    }

    // 处理柱状图数据
    let timePoints = Array.from(uniqueTimes);
    if (timePoints.length < 7) {
      // 根据时间粒度生成合适的时间点
      const generateTimePoints = () => {
        let lastTime = Math.max(...data.map(item => item.created_at));
        let points = [];
        let interval = dataExportDefaultTime === 'hour' ? 3600
          : dataExportDefaultTime === 'day' ? 86400
            : 604800;

        for (let i = 0; i < 7; i++) {
          points.push(timestamp2string1(lastTime - (i * interval), dataExportDefaultTime));
        }
        return points.reverse();
      };

      timePoints = generateTimePoints();
    }

    // 为每个时间点和模型生成数据
    timePoints.forEach(time => {
      modelList.forEach(model => {
        let existingData = data.find(item =>
          timestamp2string1(item.created_at, dataExportDefaultTime) === time &&
          item.model_name === model
        );

        newLineData.push({
          Time: time,
          Model: model,
          Usage: existingData
            ? calcUsageByItem(existingData, priceConfigOverride || modelPriceConfig, useCustomPrice)
            : 0
        });
      });
    });

    // 排序
    newPieData.sort((a, b) => b.value - a.value);
    newLineData.sort((a, b) => a.Time.localeCompare(b.Time));

    // 更新图表配置和数据
    setSpecPie(prev => ({
      ...prev,
      data: [{ id: 'id0', values: newPieData }],
      title: {
        ...prev.title,
        subtext: `总计：${renderNumber(totalTimes)}`
      },
      color: {
        specified: newModelColors
      }
    }));

    setSpecLine(prev => ({
      ...prev,
      data: [{ id: 'barData', values: newLineData }],
      title: {
        ...prev.title,
        subtext: `总计：${useCustomPrice ? renderCurrency(totalQuota) : renderQuota(totalQuota, 2)}`
      },
      tooltip: {
        mark: {
          content: [
            {
              key: (datum) => datum['Model'],
              value: (datum) =>
                useCustomPrice
                  ? renderCurrency(parseFloat(datum['Usage']))
                  : renderQuotaNumberWithDigit(parseFloat(datum['Usage']), 4),
            },
          ],
        },
        dimension: {
          content: [
            {
              key: (datum) => datum['Model'],
              value: (datum) => datum['Usage'],
            },
          ],
          updateContent: (array) => {
            array.sort((a, b) => b.value - a.value);
            let sum = 0;
            for (let i = 0; i < array.length; i++) {
              sum += parseFloat(array[i].value);
              array[i].value = useCustomPrice
                ? renderCurrency(parseFloat(array[i].value))
                : renderQuotaNumberWithDigit(parseFloat(array[i].value), 4);
            }
            array.unshift({
              key: '总计',
              value: useCustomPrice ? renderCurrency(sum) : renderQuotaNumberWithDigit(sum, 4),
            });
            return array;
          },
        },
      },
      color: {
        specified: newModelColors
      }
    }));

    setPieData(newPieData);
    setLineData(newLineData);
    setConsumeQuota(totalQuota);
    setTimes(totalTimes);
    setConsumeTokens(totalTokens);
  };

  const getUserData = async () => {
    let res = await API.get(`/api/user/self`);
    const {success, message, data} = res.data;
    if (success) {
      userDispatch({type: 'login', payload: data});
    } else {
      showError(message);
    }
  };

  useEffect(() => {
    getUserData()
    if (!initialized.current) {
      initVChartSemiTheme({
        isWatchingThemeSwitch: true,
      });
      initialized.current = true;
      loadGroupOptions().then();
      loadModelOptions().then();
      initChart();
    }
  }, []);

  useEffect(() => {
    if (!customPriceEnabled) {
      setModelOptions(allModelOptions);
    }
  }, [allModelOptions, customPriceEnabled]);

  return (
    <>
      <Layout>
        <Layout.Header>
          <h3>数据看板</h3>
        </Layout.Header>
        <Layout.Content>
          <Form ref={formRef} layout='horizontal' style={{ marginTop: 10 }}>
            <>
              <Form.DatePicker
                field='start_timestamp'
                label='起始时间'
                style={{ width: 272 }}
                initValue={start_timestamp}
                value={start_timestamp}
                type='dateTime'
                name='start_timestamp'
                onChange={(value) =>
                  handleInputChange(value, 'start_timestamp')
                }
              />
              <Form.DatePicker
                field='end_timestamp'
                fluid
                label='结束时间'
                style={{ width: 272 }}
                initValue={end_timestamp}
                value={end_timestamp}
                type='dateTime'
                name='end_timestamp'
                onChange={(value) => handleInputChange(value, 'end_timestamp')}
              />
              <Form.Select
                field='data_export_default_time'
                label='时间粒度'
                style={{ width: 176 }}
                initValue={dataExportDefaultTime}
                placeholder={'时间粒度'}
                name='data_export_default_time'
                optionList={[
                  { label: '小时', value: 'hour' },
                  { label: '天', value: 'day' },
                  { label: '周', value: 'week' },
                ]}
                onChange={(value) =>
                  handleInputChange(value, 'data_export_default_time')
                }
              ></Form.Select>
              {groupOptions.length > 0 ? (
                <Form.Select
                  field='group'
                  label='分组'
                  style={{ width: 176 }}
                  initValue={group}
                  value={group}
                  placeholder={'全部分组'}
                  name='group'
                  optionList={[
                    { label: '全部分组', value: '' },
                    ...groupOptions,
                  ]}
                  onChange={(value) => handleInputChange(value, 'group')}
                />
              ) : (
                <Form.Input
                  field='group'
                  label='分组'
                  style={{ width: 176 }}
                  value={group}
                  placeholder={'可选值'}
                  name='group'
                  onChange={(value) => handleInputChange(value, 'group')}
                />
              )}
              <Form.Input
                field='token_id'
                label='令牌 ID'
                style={{ width: 176 }}
                value={token_id}
                placeholder={'可选值'}
                name='token_id'
                onChange={(value) => handleInputChange(value, 'token_id')}
              />
              <div style={{ display: 'flex', flexDirection: 'column', marginRight: 16 }}>
                <Typography.Text style={{ marginBottom: 6 }}>模型名称</Typography.Text>
                <Select
                  placeholder='全部模型'
                  style={{ width: 280 }}
                  multiple
                  selection
                  filter
                  searchPosition='dropdown'
                  value={model_names}
                  optionList={modelOptions}
                  onChange={(value) => handleInputChange(value || [], 'model_names')}
                />
              </div>
              {isAdminUser && (
                <>
                  <Form.Input
                    field='username'
                    label='用户名称'
                    style={{ width: 176 }}
                    value={username}
                    placeholder={'可选值'}
                    name='username'
                    onChange={(value) => handleInputChange(value, 'username')}
                  />
                </>
              )}
              <Button
                label='查询'
                type='primary'
                htmlType='submit'
                className='btn-margin-right'
                onClick={refresh}
                loading={loading}
                style={{ marginTop: 24 }}
              >
                查询
              </Button>
              <Button
                type='secondary'
                onClick={openModelPriceModal}
                style={{ marginTop: 24, marginLeft: 8 }}
              >
                模型价格设置
              </Button>
              <div style={{ display: 'flex', alignItems: 'center', marginTop: 28, marginLeft: 12 }}>
                <Typography.Text style={{ marginRight: 8 }}>使用手动模型价格</Typography.Text>
                <Switch
                  checked={customPriceEnabled}
                  checkedText='开'
                  uncheckedText='关'
                  onChange={handleCustomPriceSwitchChange}
                />
              </div>
              <Form.Section>
              </Form.Section>
            </>
          </Form>
          <Spin spinning={loading}>
            <Row gutter={{ xs: 16, sm: 16, md: 16, lg: 24, xl: 24, xxl: 24 }} style={{marginTop: 20}} type="flex" justify="space-between">
              <Col span={styleState.isMobile?24:8}>
                <Card className='panel-desc-card'>
                  <Descriptions row size="small">
                    <Descriptions.Item itemKey='历史消耗'>
                      {renderQuota(userState?.user?.used_quota)}
                    </Descriptions.Item>
                    <Descriptions.Item itemKey='总请求次数'>
                      {userState.user?.request_count}
                    </Descriptions.Item>
                  </Descriptions>
                </Card>
              </Col>
              <Col span={styleState.isMobile?24:8}>
                <Card>
                  <Descriptions row size="small">
                    <Descriptions.Item itemKey={customPriceEnabled ? '统计费用(USD)' : '统计额度'}>
                      {customPriceEnabled ? renderCurrency(consumeQuota) : renderQuota(consumeQuota)}
                    </Descriptions.Item>
                    <Descriptions.Item itemKey='统计Tokens'>
                      {consumeTokens}
                    </Descriptions.Item>
                    <Descriptions.Item itemKey='统计次数'>
                      {times}
                    </Descriptions.Item>
                  </Descriptions>
                </Card>
              </Col>
              <Col span={styleState.isMobile ? 24 : 8}>
                <Card>
                  <Descriptions row size='small'>
                    <Descriptions.Item itemKey='平均RPM'>
                      {(times /
                        ((Date.parse(end_timestamp) -
                            Date.parse(start_timestamp)) /
                          60000)).toFixed(3)}
                    </Descriptions.Item>
                    <Descriptions.Item itemKey='平均TPM'>
                      {(consumeTokens /
                        ((Date.parse(end_timestamp) -
                            Date.parse(start_timestamp)) /
                          60000)).toFixed(3)}
                    </Descriptions.Item>
                  </Descriptions>
                </Card>
              </Col>
            </Row>
            <Card style={{marginTop: 20}}>
              <Tabs type="line" defaultActiveKey="1">
                <Tabs.TabPane tab="消耗分布" itemKey="1">
                  <div style={{ height: 500 }}>
                    <VChart
                      spec={spec_line}
                      option={{ mode: "desktop-browser" }}
                    />
                  </div>
                </Tabs.TabPane>
                <Tabs.TabPane tab="调用次数分布" itemKey="2">
                  <div style={{ height: 500 }}>
                    <VChart
                      spec={spec_pie}
                      option={{ mode: "desktop-browser" }}
                    />
                  </div>
                </Tabs.TabPane>

              </Tabs>
            </Card>
          </Spin>
          <Modal
            title='模型价格设置'
            visible={modelPriceModalVisible}
            onOk={saveModelPriceConfig}
            onCancel={() => setModelPriceModalVisible(false)}
            okText='保存并应用'
            cancelText='取消'
            width={styleState.isMobile ? '96%' : 900}
          >
            <Typography.Text type='secondary'>
              单位：USD / 1M tokens。未配置模型将按 0 计费。
            </Typography.Text>
            <div style={{ marginTop: 12, maxHeight: 460, overflowY: 'auto' }}>
              {Object.keys(editingModelPrices).length === 0 ? (
                <Typography.Text>当前查询结果中暂无可配置模型，请先查询到有数据的模型后再设置。</Typography.Text>
              ) : (
                Object.keys(editingModelPrices).sort().map((modelName) => (
                  <div
                    key={modelName}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 12,
                      marginBottom: 10,
                    }}
                  >
                    <Typography.Text
                      ellipsis={{ showTooltip: true }}
                      style={{ width: styleState.isMobile ? 120 : 220, flexShrink: 0 }}
                    >
                      {modelName}
                    </Typography.Text>
                    <Input
                      type='number'
                      min={0}
                      step='0.0001'
                      value={editingModelPrices[modelName]?.input_price}
                      placeholder='输入价格'
                      suffix='输入'
                      onChange={(value) => updateEditingModelPrice(modelName, 'input_price', value)}
                    />
                    <Input
                      type='number'
                      min={0}
                      step='0.0001'
                      value={editingModelPrices[modelName]?.output_price}
                      placeholder='输出价格'
                      suffix='输出'
                      onChange={(value) => updateEditingModelPrice(modelName, 'output_price', value)}
                    />
                  </div>
                ))
              )}
            </div>
          </Modal>
        </Layout.Content>
      </Layout>
    </>
  );
};

export default Detail;
