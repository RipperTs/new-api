import React, { useEffect, useState, useRef } from 'react';
import { Button, Col, Form, Row, Select, Spin } from '@douyinfe/semi-ui';
import { API, showError, showSuccess, showWarning } from '../../../helpers';
import { useTranslation } from 'react-i18next';

const defaultInputs = {
  ChannelDisableThreshold: '',
  QuotaRemindThreshold: '',
  AutomaticDisableChannelEnabled: false,
  AutomaticEnableChannelEnabled: false,
  EmailNotificationEnabled: true,
  EmailNotificationGroups: [],
  FeishuNotificationEnabled: false,
  FeishuNotificationGroups: [],
  DingTalkNotificationEnabled: false,
  DingTalkNotificationGroups: [],
};

const parseNotificationGroups = (value) => {
  if (!value) {
    return [];
  }
  if (Array.isArray(value)) {
    return value
      .map((item) => String(item).trim())
      .filter((item) => item !== '');
  }
  if (typeof value !== 'string') {
    return [];
  }
  return value
    .split(/[;,]/)
    .map((item) => item.trim())
    .filter((item) => item !== '');
};

export default function SettingsMonitoring(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState(defaultInputs);
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(defaultInputs);
  const [groupOptions, setGroupOptions] = useState([]);

  function onSubmit() {
    const normalizedInputs = {
      ...inputs,
      EmailNotificationGroups: parseNotificationGroups(
        inputs.EmailNotificationGroups,
      ).join(','),
      FeishuNotificationGroups: parseNotificationGroups(
        inputs.FeishuNotificationGroups,
      ).join(','),
      DingTalkNotificationGroups: parseNotificationGroups(
        inputs.DingTalkNotificationGroups,
      ).join(','),
    };
    const normalizedInputsRow = {
      ...inputsRow,
      EmailNotificationGroups: parseNotificationGroups(
        inputsRow.EmailNotificationGroups,
      ).join(','),
      FeishuNotificationGroups: parseNotificationGroups(
        inputsRow.FeishuNotificationGroups,
      ).join(','),
      DingTalkNotificationGroups: parseNotificationGroups(
        inputsRow.DingTalkNotificationGroups,
      ).join(','),
    };
    const updateArray = Object.keys(normalizedInputs).reduce((result, key) => {
      if (normalizedInputs[key] !== normalizedInputsRow[key]) {
        result.push(key);
      }
      return result;
    }, []);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    const requestQueue = updateArray.map((key) => {
      let value = '';
      if (typeof normalizedInputs[key] === 'boolean') {
        value = String(normalizedInputs[key]);
      } else {
        value = normalizedInputs[key];
      }
      return API.put('/api/option/', {
        key,
        value,
      });
    });
    setLoading(true);
    Promise.all(requestQueue)
      .then((res) => {
        if (requestQueue.length === 1) {
          if (res.includes(undefined)) return;
        } else if (requestQueue.length > 1) {
          if (res.includes(undefined))
            return showError(t('部分保存失败，请重试'));
        }
        showSuccess(t('保存成功'));
        props.refresh();
      })
      .catch(() => {
        showError(t('保存失败，请重试'));
      })
      .finally(() => {
        setLoading(false);
      });
  }

  const fetchGroups = async () => {
    try {
      const res = await API.get('/api/group/');
      setGroupOptions(
        res.data.data.map((group) => ({
          label: group,
          value: group,
        })),
      );
    } catch (error) {
      showError(error.message);
    }
  };

  useEffect(() => {
    const currentInputs = { ...defaultInputs };
    Object.keys(defaultInputs).forEach((key) => {
      if (props.options[key] !== undefined) {
        currentInputs[key] = props.options[key];
      }
    });
    currentInputs.EmailNotificationGroups = parseNotificationGroups(
      currentInputs.EmailNotificationGroups,
    );
    currentInputs.FeishuNotificationGroups = parseNotificationGroups(
      currentInputs.FeishuNotificationGroups,
    );
    currentInputs.DingTalkNotificationGroups = parseNotificationGroups(
      currentInputs.DingTalkNotificationGroups,
    );
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    refForm.current.setValues(currentInputs);
  }, [props.options]);

  useEffect(() => {
    fetchGroups().then();
  }, []);

  return (
    <>
      <Spin spinning={loading}>
        <Form
          values={inputs}
          getFormApi={(formAPI) => (refForm.current = formAPI)}
          style={{ marginBottom: 15 }}
        >
          <Form.Section text={t('监控设置')}>
            <Row gutter={16}>
              <Col span={8}>
                <Form.InputNumber
                  label={t('最长响应时间')}
                  step={1}
                  min={0}
                  suffix={t('秒')}
                  extraText={t(
                    '当运行通道全部测试时，超过此时间将自动禁用通道',
                  )}
                  placeholder={''}
                  field={'ChannelDisableThreshold'}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      ChannelDisableThreshold: String(value),
                    })
                  }
                />
              </Col>
              <Col span={8}>
                <Form.InputNumber
                  label={t('额度提醒阈值')}
                  step={1}
                  min={0}
                  suffix={'Token'}
                  extraText={t('低于此额度时将发送邮件提醒用户')}
                  placeholder={''}
                  field={'QuotaRemindThreshold'}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      QuotaRemindThreshold: String(value),
                    })
                  }
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col span={8}>
                <Form.Switch
                  field={'AutomaticDisableChannelEnabled'}
                  label={t('失败时自动禁用通道')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) => {
                    setInputs({
                      ...inputs,
                      AutomaticDisableChannelEnabled: value,
                    });
                  }}
                />
              </Col>
              <Col span={8}>
                <Form.Switch
                  field={'AutomaticEnableChannelEnabled'}
                  label={t('成功时自动启用通道')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      AutomaticEnableChannelEnabled: value,
                    })
                  }
                />
              </Col>
              <Col span={8}>
                <Form.Switch
                  field={'EmailNotificationEnabled'}
                  label={t('启用邮箱异常通知')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      EmailNotificationEnabled: value,
                    })
                  }
                />
              </Col>
              <Col span={8}>
                <Form.Switch
                  field={'FeishuNotificationEnabled'}
                  label={t('启用飞书异常通知')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      FeishuNotificationEnabled: value,
                    })
                  }
                />
              </Col>
              <Col span={8}>
                <Form.Switch
                  field={'DingTalkNotificationEnabled'}
                  label={t('启用钉钉异常通知')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      DingTalkNotificationEnabled: value,
                    })
                  }
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col span={16}>
                <div
                  style={{ marginBottom: 8, color: 'var(--semi-color-text-2)' }}
                >
                  {t('告警分组')}
                </div>
                <Select
                  multiple
                  search
                  placeholder={t('留空表示全部分组')}
                  optionList={groupOptions}
                  value={inputs.EmailNotificationGroups}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      EmailNotificationGroups: value || [],
                    })
                  }
                />
                <div
                  style={{
                    marginTop: 8,
                    color: 'var(--semi-color-text-2)',
                    fontSize: 12,
                  }}
                >
                  {t(
                    '仅当这些分组发生渠道异常时发送邮箱告警，留空表示全部分组',
                  )}
                </div>
              </Col>
            </Row>
            <Row gutter={16}>
              <Col span={16}>
                <div
                  style={{ marginBottom: 8, color: 'var(--semi-color-text-2)' }}
                >
                  {t('飞书告警分组')}
                </div>
                <Select
                  multiple
                  search
                  placeholder={t('留空表示全部分组')}
                  optionList={groupOptions}
                  value={inputs.FeishuNotificationGroups}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      FeishuNotificationGroups: value || [],
                    })
                  }
                />
                <div
                  style={{
                    marginTop: 8,
                    color: 'var(--semi-color-text-2)',
                    fontSize: 12,
                  }}
                >
                  {t(
                    '仅当这些分组发生渠道异常时发送飞书告警，留空表示全部分组',
                  )}
                </div>
              </Col>
            </Row>
            <Row gutter={16}>
              <Col span={16}>
                <div
                  style={{ marginBottom: 8, color: 'var(--semi-color-text-2)' }}
                >
                  {t('钉钉告警分组')}
                </div>
                <Select
                  multiple
                  search
                  placeholder={t('留空表示全部分组')}
                  optionList={groupOptions}
                  value={inputs.DingTalkNotificationGroups}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      DingTalkNotificationGroups: value || [],
                    })
                  }
                />
                <div
                  style={{
                    marginTop: 8,
                    color: 'var(--semi-color-text-2)',
                    fontSize: 12,
                  }}
                >
                  {t(
                    '仅当这些分组发生渠道异常时发送钉钉告警，留空表示全部分组',
                  )}
                </div>
              </Col>
            </Row>
            <Row>
              <Button size='default' onClick={onSubmit}>
                {t('保存监控设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}
