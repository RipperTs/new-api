import React, { useEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  API,
  isMobile,
  showError,
  showInfo,
  showSuccess,
  verifyJSON,
} from '../../helpers';
import { CHANNEL_OPTIONS } from '../../constants';
import Title from '@douyinfe/semi-ui/lib/es/typography/title';
import {
  SideSheet,
  Space,
  Spin,
  Button,
  Tooltip,
  Input,
  Typography,
  Select,
  TextArea,
  Checkbox,
  Banner,
} from '@douyinfe/semi-ui';
import { getChannelModels, loadChannelModels } from '../../components/utils.js';

const MODEL_MAPPING_EXAMPLE = {
  'gpt-3.5-turbo': 'gpt-3.5-turbo-0125',
};

const STATUS_CODE_MAPPING_EXAMPLE = {
  400: '500',
};

const REGION_EXAMPLE = {
  default: 'us-central1',
  'claude-3-5-sonnet-20240620': 'europe-west1',
};

const fetchButtonTips =
  '1. 新建渠道时，请求通过当前浏览器发出；2. 编辑已有渠道，请求通过后端服务器发出';
const CODEX_OFFICIAL_BASE_URL = 'https://chatgpt.com/backend-api';
const CLAUDE_OFFICIAL_BASE_URL = 'https://claude.ai';
const CLAUDE_DEEPSEEK_V4_BASE_URL = 'https://api.deepseek.com/anthropic';
const CLAUDE_DEEPSEEK_V4_MODE = 'deepseek_v4';

function type2secretPrompt(type) {
  // inputs.type === 15 ? '按照如下格式输入：APIKey|SecretKey' : (inputs.type === 18 ? '按照如下格式输入：APPID|APISecret|APIKey' : '请输入渠道对应的鉴权密钥')
  switch (type) {
    case 15:
      return '按照如下格式输入：APIKey|SecretKey';
    case 18:
      return '按照如下格式输入：APPID|APISecret|APIKey';
    case 22:
      return '按照如下格式输入：APIKey-AppId，例如：fastgpt-0sp2gtvfdgyi4k30jwlgwf1i-64f335d84283f05518e9e041';
    case 23:
      return '按照如下格式输入：AppId|SecretId|SecretKey';
    case 33:
      return '按照如下格式输入：Ak|Sk|Region';
    case 45:
      return '请输入 Codex 的鉴权秘钥 (非Access Token)';
    case 44:
      return '请输入 Claude Code 的鉴权秘钥';
    default:
      return '请输入渠道对应的鉴权密钥';
  }
}

function safeParseJSON(str) {
  if (!str || typeof str !== 'string') return {};
  const s = str.trim();
  if (!s) return {};
  try {
    const obj = JSON.parse(s);
    if (obj && typeof obj === 'object' && !Array.isArray(obj)) return obj;
    return {};
  } catch (e) {
    return {};
  }
}

function getRelatedModelsByType(type) {
  switch (type) {
    case 2:
      return [
        'mj_imagine',
        'mj_variation',
        'mj_reroll',
        'mj_blend',
        'mj_upscale',
        'mj_describe',
        'mj_uploads',
      ];
    case 5:
      return [
        'swap_face',
        'mj_imagine',
        'mj_variation',
        'mj_reroll',
        'mj_blend',
        'mj_upscale',
        'mj_describe',
        'mj_zoom',
        'mj_shorten',
        'mj_modal',
        'mj_inpaint',
        'mj_custom_zoom',
        'mj_high_variation',
        'mj_low_variation',
        'mj_pan',
        'mj_uploads',
      ];
    case 36:
      return ['suno_music', 'suno_lyrics'];
    default:
      return getChannelModels(type);
  }
}

const EditChannel = (props) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const channelId = props.editingChannel.id;
  const isEdit = channelId !== undefined;
  const [loading, setLoading] = useState(isEdit);
  const handleCancel = () => {
    props.handleClose();
  };
  const originInputs = {
    name: '',
    type: 1,
    key: '',
    openai_organization: '',
    max_input_tokens: 0,
    base_url: '',
    other: '',
    model_mapping: '',
    status_code_mapping: '',
    models: [],
    auto_ban: 1,
    test_model: '',
    groups: ['default'],
    priority: 0,
    weight: 0,
    tag: '',
    proxy_url: '',
    setting: '',
  };
  const [batch, setBatch] = useState(false);
  const [autoBan, setAutoBan] = useState(true);
  // const [autoBan, setAutoBan] = useState(true);
  const [inputs, setInputs] = useState(originInputs);
  const [originModelOptions, setOriginModelOptions] = useState([]);
  const [modelOptions, setModelOptions] = useState([]);
  const [groupOptions, setGroupOptions] = useState([]);
  const [basicModels, setBasicModels] = useState([]);
  const [fullModels, setFullModels] = useState([]);
  const [customModel, setCustomModel] = useState('');
  // 模型重定向行编辑：[{ from: string, to: string }]
  const [modelMappingRows, setModelMappingRows] = useState([]);
  const [codexAuthStatus, setCodexAuthStatus] = useState(null);
  const [codexAuthLoading, setCodexAuthLoading] = useState(false);
  const [codexCallbackUrl, setCodexCallbackUrl] = useState('');
  const [claudeAuthStatus, setClaudeAuthStatus] = useState(null);
  const [claudeAuthLoading, setClaudeAuthLoading] = useState(false);
  const [claudeCallbackUrl, setClaudeCallbackUrl] = useState('');
  const codexOAuthDoneRef = useRef(false);
  const claudeOAuthDoneRef = useRef(false);

  // 工具：JSON字符串 -> 行
  const parseModelMappingToRows = (jsonStr) => {
    if (!jsonStr || typeof jsonStr !== 'string' || jsonStr.trim() === '')
      return [];
    try {
      const obj = JSON.parse(jsonStr);
      if (obj && typeof obj === 'object' && !Array.isArray(obj)) {
        return Object.entries(obj).map(([from, to]) => ({
          from,
          to: String(to),
        }));
      }
    } catch (e) {
      // ignore parse error, keep empty rows
    }
    return [];
  };

  // 工具：行 -> 美化后的 JSON 字符串（空则返回空串）
  const rowsToJSONString = (rows) => {
    const obj = {};
    rows.forEach((r) => {
      const from = (r.from || '').trim();
      const to = (r.to || '').trim();
      if (from && to) obj[from] = to;
    });
    const keys = Object.keys(obj);
    if (keys.length === 0) return '';
    return JSON.stringify(obj, null, 2);
  };

  // 同步：当服务端载入的 model_mapping 变化时，刷新行编辑器
  useEffect(() => {
    setModelMappingRows(parseModelMappingToRows(inputs.model_mapping));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inputs.model_mapping]);

  // 行变更时，同时回填 inputs.model_mapping，保持提交逻辑不变
  const updateRows = (rows) => {
    setModelMappingRows(rows);
    const json = rowsToJSONString(rows);
    handleInputChange('model_mapping', json);
  };

  const addMappingRow = () => {
    updateRows([...(modelMappingRows || []), { from: '', to: '' }]);
  };

  const removeMappingRow = (idx) => {
    const next = [...modelMappingRows];
    next.splice(idx, 1);
    updateRows(next);
  };

  const changeRow = (idx, field, value) => {
    const next = [...modelMappingRows];
    next[idx] = { ...next[idx], [field]: value };
    updateRows(next);
  };
  const handleInputChange = (name, value) => {
    if (name === 'type') {
      const localModels = getRelatedModelsByType(value);
      setBasicModels(localModels);
      setInputs((prev) => {
        const next = { ...prev, [name]: value };
        if ((prev.models || []).length === 0) next.models = localModels;
        return next;
      });
      return;
    }
    if (name === 'setting') {
      const normalized = typeof value === 'string' ? value : '';
      setInputs((inputs) => ({ ...inputs, [name]: normalized }));
      return;
    }
    setInputs((inputs) => ({ ...inputs, [name]: value }));
    //setAutoBan
  };

  const loadChannel = async () => {
    setLoading(true);
    let res = await API.get(`/api/channel/${channelId}`);
    if (res === undefined) {
      return;
    }
    const { success, message, data } = res.data;
    if (success) {
      if (data.models === '') {
        data.models = [];
      } else {
        data.models = data.models.split(',');
      }
      if (data.group === '') {
        data.groups = [];
      } else {
        data.groups = data.group.split(',');
      }
      if (data.model_mapping !== '') {
        data.model_mapping = JSON.stringify(
          JSON.parse(data.model_mapping),
          null,
          2,
        );
      }
      // 初始化行编辑器
      setModelMappingRows(parseModelMappingToRows(data.model_mapping));
      setInputs(data);
      if (data.auto_ban === 0) {
        setAutoBan(false);
      } else {
        setAutoBan(true);
      }
      setBasicModels(getRelatedModelsByType(data.type));
      // console.log(data);
    } else {
      showError(message);
    }
    setLoading(false);
  };

  const getCodexSetting = () => safeParseJSON(inputs.setting);
  const getCodexAuthMode = () => {
    const s = getCodexSetting();
    return s.auth_mode === 'oauth' ? 'oauth' : 'api_key';
  };
  const getCodexSessionId = () => {
    const s = getCodexSetting();
    return s.codex_oauth_session_id || '';
  };
  const applyCodexAuthMode = (mode) => {
    setInputs((prev) => {
      const s = safeParseJSON(prev.setting);
      const next = { ...s };
      if (mode === 'oauth') {
        next.auth_mode = 'oauth';
        return {
          ...prev,
          base_url: CODEX_OFFICIAL_BASE_URL,
          setting: JSON.stringify(next, null, 2),
        };
      }
      next.auth_mode = 'api_key';
      delete next.codex_oauth_session_id;
      delete next.codex_email;
      return { ...prev, setting: JSON.stringify(next, null, 2) };
    });
    if (mode !== 'oauth') {
      setCodexAuthStatus(null);
      setCodexCallbackUrl('');
    }
  };

  const getClaudeSetting = () => safeParseJSON(inputs.setting);
  const getClaudeAuthMode = () => {
    const s = getClaudeSetting();
    return s.auth_mode === 'oauth' ? 'oauth' : 'api_key';
  };
  const getClaudeSessionId = () => {
    const s = getClaudeSetting();
    return s.claude_oauth_session_id || '';
  };
  const getClaudeUseAnthropicBeta = () => {
    const s = getClaudeSetting();
    if (typeof s.use_anthropic_beta === 'boolean') {
      return s.use_anthropic_beta;
    }
    return true;
  };
  const getClaudeSimulateCLI = () => {
    const s = getClaudeSetting();
    if (typeof s.simulate_claude_code_cli === 'boolean') {
      return s.simulate_claude_code_cli;
    }
    return false;
  };
  const getClaudeDeepSeekV4Mode = () => {
    const s = getClaudeSetting();
    return s.compatibility_mode === CLAUDE_DEEPSEEK_V4_MODE;
  };
  const applyClaudeUseAnthropicBeta = (enabled) => {
    setInputs((prev) => {
      const s = safeParseJSON(prev.setting);
      const next = { ...s, use_anthropic_beta: !!enabled };
      return { ...prev, setting: JSON.stringify(next, null, 2) };
    });
  };
  const applyClaudeSimulateCLI = (enabled) => {
    setInputs((prev) => {
      const s = safeParseJSON(prev.setting);
      const next = { ...s, simulate_claude_code_cli: !!enabled };
      return { ...prev, setting: JSON.stringify(next, null, 2) };
    });
  };
  const applyClaudeDeepSeekV4Mode = (enabled) => {
    setInputs((prev) => {
      const s = safeParseJSON(prev.setting);
      const next = { ...s };
      if (enabled) {
        next.compatibility_mode = CLAUDE_DEEPSEEK_V4_MODE;
        next.auth_mode = 'api_key';
        delete next.claude_oauth_session_id;
        delete next.claude_email;
        return {
          ...prev,
          base_url: CLAUDE_DEEPSEEK_V4_BASE_URL,
          setting: JSON.stringify(next, null, 2),
        };
      }
      delete next.compatibility_mode;
      return { ...prev, setting: JSON.stringify(next, null, 2) };
    });
    if (enabled) {
      setClaudeAuthStatus(null);
      setClaudeCallbackUrl('');
    }
  };
  const applyClaudeAuthMode = (mode) => {
    setInputs((prev) => {
      const s = safeParseJSON(prev.setting);
      const next = { ...s };
      if (mode === 'oauth') {
        next.auth_mode = 'oauth';
        delete next.compatibility_mode;
        return {
          ...prev,
          base_url: CLAUDE_OFFICIAL_BASE_URL,
          setting: JSON.stringify(next, null, 2),
        };
      }
      next.auth_mode = 'api_key';
      delete next.claude_oauth_session_id;
      delete next.claude_email;
      return { ...prev, setting: JSON.stringify(next, null, 2) };
    });
    if (mode !== 'oauth') {
      setClaudeAuthStatus(null);
      setClaudeCallbackUrl('');
    }
  };

  const refreshCodexAuthStatus = async () => {
    if (inputs.type !== 45) return;
    const mode = getCodexAuthMode();
    if (mode !== 'oauth') {
      setCodexAuthStatus(null);
      return;
    }
    setCodexAuthLoading(true);
    try {
      if (isEdit) {
        const res = await API.get(
          `/api/channel/${channelId}/codex_auth/status`,
        );
        if (res?.data?.success) setCodexAuthStatus(res.data.data);
      } else {
        const sid = getCodexSessionId();
        if (!sid) {
          setCodexAuthStatus(null);
        } else {
          const res = await API.get(`/api/channel/codex_auth/session/${sid}`);
          if (res?.data?.success) setCodexAuthStatus(res.data.data);
        }
      }
    } catch (e) {
      // ignore
    } finally {
      setCodexAuthLoading(false);
    }
  };

  useEffect(() => {
    if (inputs.type === 45 && getCodexAuthMode() === 'oauth') {
      refreshCodexAuthStatus().then();
    }
  }, [inputs.type, inputs.setting, isEdit]);

  const refreshClaudeAuthStatus = async () => {
    if (inputs.type !== 44) return;
    const mode = getClaudeAuthMode();
    if (mode !== 'oauth') {
      setClaudeAuthStatus(null);
      return;
    }
    setClaudeAuthLoading(true);
    try {
      if (isEdit) {
        const res = await API.get(
          `/api/channel/${channelId}/claude_auth/status`,
        );
        if (res?.data?.success) setClaudeAuthStatus(res.data.data);
      } else {
        const sid = getClaudeSessionId();
        if (!sid) {
          setClaudeAuthStatus(null);
        } else {
          const res = await API.get(`/api/channel/claude_auth/session/${sid}`);
          if (res?.data?.success) setClaudeAuthStatus(res.data.data);
        }
      }
    } catch (e) {
      // ignore
    } finally {
      setClaudeAuthLoading(false);
    }
  };

  useEffect(() => {
    if (inputs.type === 44 && getClaudeAuthMode() === 'oauth') {
      refreshClaudeAuthStatus().then();
    }
  }, [inputs.type, inputs.setting, isEdit]);

  const startCodexOAuth = async () => {
    setCodexAuthLoading(true);
    try {
      codexOAuthDoneRef.current = false;
      const res = await API.post('/api/channel/codex_auth/start', {
        channel_id: isEdit ? parseInt(channelId) : 0,
        proxy_url: inputs.proxy_url || '',
      });
      if (!res?.data?.success) {
        showError(res?.data?.message || '启动 Codex 授权失败');
        return;
      }
      const { auth_url, session_id } = res.data.data || {};
      if (!auth_url || !session_id) {
        showError('启动 Codex 授权失败');
        return;
      }
      if (!isEdit) {
        setInputs((prev) => {
          const s = safeParseJSON(prev.setting);
          const next = {
            ...s,
            auth_mode: 'oauth',
            codex_oauth_session_id: session_id,
          };
          return {
            ...prev,
            base_url: CODEX_OFFICIAL_BASE_URL,
            setting: JSON.stringify(next, null, 2),
          };
        });
      }
      window.open(auth_url, '_blank', 'noopener,noreferrer');
    } finally {
      setCodexAuthLoading(false);
    }
  };

  const completeCodexOAuth = async () => {
    const cb = (codexCallbackUrl || '').trim();
    if (!cb) {
      showInfo('请粘贴回调 URL');
      return;
    }
    setCodexAuthLoading(true);
    try {
      const res = await API.post('/api/channel/codex_auth/complete', {
        callback_url: cb,
      });
      if (!res?.data?.success) {
        showError(res?.data?.message || '完成 Codex 授权失败');
        return;
      }
      const { session_id, bound } = res.data.data || {};
      codexOAuthDoneRef.current = true;
      if (isEdit) {
        if (!bound && session_id) {
          await API.post(`/api/channel/${channelId}/codex_auth/bind`, {
            session_id,
          });
        }
        await loadChannel();
      } else if (session_id) {
        setInputs((prev) => {
          const s = safeParseJSON(prev.setting);
          const next = {
            ...s,
            auth_mode: 'oauth',
            codex_oauth_session_id: session_id,
          };
          return {
            ...prev,
            base_url: CODEX_OFFICIAL_BASE_URL,
            setting: JSON.stringify(next, null, 2),
          };
        });
      }
      await refreshCodexAuthStatus();
      setCodexCallbackUrl('');
      showSuccess('Codex 授权完成');
    } finally {
      setCodexAuthLoading(false);
    }
  };

  const startClaudeOAuth = async () => {
    setClaudeAuthLoading(true);
    try {
      claudeOAuthDoneRef.current = false;
      const res = await API.post('/api/channel/claude_auth/start', {
        channel_id: isEdit ? parseInt(channelId) : 0,
        proxy_url: inputs.proxy_url || '',
      });
      if (!res?.data?.success) {
        showError(res?.data?.message || '启动 Claude 授权失败');
        return;
      }
      const { auth_url, session_id } = res.data.data || {};
      if (!auth_url || !session_id) {
        showError('启动 Claude 授权失败');
        return;
      }
      if (!isEdit) {
        setInputs((prev) => {
          const s = safeParseJSON(prev.setting);
          const next = {
            ...s,
            auth_mode: 'oauth',
            claude_oauth_session_id: session_id,
          };
          return {
            ...prev,
            base_url: CLAUDE_OFFICIAL_BASE_URL,
            setting: JSON.stringify(next, null, 2),
          };
        });
      }
      window.open(auth_url, '_blank', 'noopener,noreferrer');
    } finally {
      setClaudeAuthLoading(false);
    }
  };

  const completeClaudeOAuth = async () => {
    const cb = (claudeCallbackUrl || '').trim();
    if (!cb) {
      showInfo('请粘贴回调 URL');
      return;
    }
    setClaudeAuthLoading(true);
    try {
      const res = await API.post('/api/channel/claude_auth/complete', {
        callback_url: cb,
      });
      if (!res?.data?.success) {
        showError(res?.data?.message || '完成 Claude 授权失败');
        return;
      }
      const { session_id, bound } = res.data.data || {};
      claudeOAuthDoneRef.current = true;
      if (isEdit) {
        if (!bound && session_id) {
          await API.post(`/api/channel/${channelId}/claude_auth/bind`, {
            session_id,
          });
        }
        await loadChannel();
      } else if (session_id) {
        setInputs((prev) => {
          const s = safeParseJSON(prev.setting);
          const next = {
            ...s,
            auth_mode: 'oauth',
            claude_oauth_session_id: session_id,
          };
          return {
            ...prev,
            base_url: CLAUDE_OFFICIAL_BASE_URL,
            setting: JSON.stringify(next, null, 2),
          };
        });
      }
      await refreshClaudeAuthStatus();
      setClaudeCallbackUrl('');
      showSuccess('Claude 授权完成');
    } finally {
      setClaudeAuthLoading(false);
    }
  };

  const fetchUpstreamModelList = async (name) => {
    // if (inputs['type'] !== 1) {
    //   showError(t('仅支持 OpenAI 接口格式'));
    //   return;
    // }
    setLoading(true);
    const models = inputs['models'] || [];
    let err = false;

    if (isEdit) {
      // 如果是编辑模式，使用已有的channel id获取模型列表
      const res = await API.get('/api/channel/fetch_models/' + channelId);
      if (res.data && res.data?.success) {
        models.push(...res.data.data);
      } else {
        err = true;
      }
    } else {
      // 如果是新建模式，通过后端代理获取模型列表
      if (inputs.type === 45 && getCodexAuthMode() === 'oauth') {
        showError(t('Codex Auth 模式下暂不支持自动拉取模型列表，请手动选择'));
        err = true;
      } else if (inputs.type === 44 && getClaudeAuthMode() === 'oauth') {
        showError(t('Claude Auth 模式下暂不支持自动拉取模型列表，请手动选择'));
        err = true;
      } else if (!inputs?.['key']) {
        showError(t('请填写密钥'));
        err = true;
      } else {
        try {
          const res = await API.post('/api/channel/fetch_models', {
            base_url: inputs['base_url'],
            type: inputs['type'],
            key: inputs['key'],
            proxy_url: inputs['proxy_url'],
          });

          if (res.data && res.data.success) {
            models.push(...res.data.data);
          } else {
            err = true;
          }
        } catch (error) {
          console.error('Error fetching models:', error);
          err = true;
        }
      }
    }

    if (!err) {
      handleInputChange(name, Array.from(new Set(models)));
      showSuccess(t('获取模型列表成功'));
    } else {
      showError(t('获取模型列表失败'));
    }
    setLoading(false);
  };

  const fetchModels = async () => {
    try {
      let res = await API.get(`/api/channel/models`);
      let localModelOptions = res.data.data.map((model) => ({
        label: model.id,
        value: model.id,
      }));
      setOriginModelOptions(localModelOptions);
      setFullModels(res.data.data.map((model) => model.id));
    } catch (error) {
      showError(error.message);
    }
  };

  const fetchGroups = async () => {
    try {
      let res = await API.get(`/api/group/`);
      if (res === undefined) {
        return;
      }
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
    let localModelOptions = [...originModelOptions];
    inputs.models.forEach((model) => {
      if (!localModelOptions.find((option) => option.label === model)) {
        localModelOptions.push({
          label: model,
          value: model,
        });
      }
    });
    setModelOptions(localModelOptions);
  }, [originModelOptions, inputs.models]);

  useEffect(() => {
    fetchModels().then();
    fetchGroups().then();
    if (isEdit) {
      loadChannel().then(() => {});
    } else {
      const localModels = getRelatedModelsByType(originInputs.type);
      setBasicModels(localModels);
      setInputs({ ...originInputs, models: localModels });
    }
  }, [props.editingChannel.id]);

  useEffect(() => {
    const handler = async (event) => {
      if (!event?.data) return;
      if (event.data.type === 'CODEX_OAUTH_DONE') {
        if (inputs.type !== 45) return;
        if (codexOAuthDoneRef.current) return;
        const { session_id, bound } = event.data || {};
        codexOAuthDoneRef.current = true;
        if (isEdit) {
          if (!bound && session_id) {
            await API.post(`/api/channel/${channelId}/codex_auth/bind`, {
              session_id,
            });
          }
          await loadChannel();
        } else if (session_id) {
          setInputs((prev) => {
            const s = safeParseJSON(prev.setting);
            const next = {
              ...s,
              auth_mode: 'oauth',
              codex_oauth_session_id: session_id,
            };
            return {
              ...prev,
              base_url: CODEX_OFFICIAL_BASE_URL,
              setting: JSON.stringify(next, null, 2),
            };
          });
        }
        await refreshCodexAuthStatus();
        showSuccess('Codex 授权完成');
      }
      if (event.data.type === 'CLAUDE_OAUTH_DONE') {
        if (inputs.type !== 44) return;
        if (claudeOAuthDoneRef.current) return;
        const { session_id, bound } = event.data || {};
        claudeOAuthDoneRef.current = true;
        if (isEdit) {
          if (!bound && session_id) {
            await API.post(`/api/channel/${channelId}/claude_auth/bind`, {
              session_id,
            });
          }
          await loadChannel();
        } else if (session_id) {
          setInputs((prev) => {
            const s = safeParseJSON(prev.setting);
            const next = {
              ...s,
              auth_mode: 'oauth',
              claude_oauth_session_id: session_id,
            };
            return {
              ...prev,
              base_url: CLAUDE_OFFICIAL_BASE_URL,
              setting: JSON.stringify(next, null, 2),
            };
          });
        }
        await refreshClaudeAuthStatus();
        showSuccess('Claude 授权完成');
      }
    };
    window.addEventListener('message', handler);
    return () => window.removeEventListener('message', handler);
  }, [inputs.type, isEdit, channelId, inputs.setting]);

  const submit = async () => {
    const codexMode = inputs.type === 45 ? getCodexAuthMode() : 'api_key';
    const claudeMode = inputs.type === 44 ? getClaudeAuthMode() : 'api_key';
    if (!isEdit && inputs.name === '') {
      showInfo(t('请填写渠道名称！'));
      return;
    }
    if (isEdit && inputs.type === 45 && codexMode === 'oauth') {
      if (!codexAuthStatus || !codexAuthStatus.has_refresh) {
        showInfo(t('请先完成 Codex 授权登录并绑定渠道！'));
        return;
      }
    }
    if (isEdit && inputs.type === 44 && claudeMode === 'oauth') {
      if (!claudeAuthStatus || !claudeAuthStatus.has_refresh) {
        showInfo(t('请先完成 Claude 授权登录并绑定渠道！'));
        return;
      }
    }
    if (!isEdit && inputs.type === 45 && codexMode === 'oauth') {
      const sid = getCodexSessionId();
      if (!sid) {
        showInfo(t('请先完成 Codex 授权登录！'));
        return;
      }
    }
    if (!isEdit && inputs.type === 44 && claudeMode === 'oauth') {
      const sid = getClaudeSessionId();
      if (!sid) {
        showInfo(t('请先完成 Claude 授权登录！'));
        return;
      }
    }
    if (
      !isEdit &&
      inputs.key === '' &&
      !(inputs.type === 45 && codexMode === 'oauth') &&
      !(inputs.type === 44 && claudeMode === 'oauth')
    ) {
      showInfo(t('请填写渠道名称和渠道密钥！'));
      return;
    }
    if (inputs.models.length === 0) {
      showInfo(t('请至少选择一个模型！'));
      return;
    }
    if (inputs.model_mapping !== '' && !verifyJSON(inputs.model_mapping)) {
      showInfo(t('模型映射必须是合法的 JSON 格式！'));
      return;
    }
    let localInputs = { ...inputs };
    if (typeof localInputs.setting === 'string') {
      localInputs.setting = localInputs.setting.trim();
    } else {
      localInputs.setting = '';
    }
    if (localInputs.base_url && localInputs.base_url.endsWith('/')) {
      localInputs.base_url = localInputs.base_url.slice(
        0,
        localInputs.base_url.length - 1,
      );
    }
    if (localInputs.type === 3 && localInputs.other === '') {
      localInputs.other = '2023-06-01-preview';
    }
    if (localInputs.type === 18 && localInputs.other === '') {
      localInputs.other = 'v2.1';
    }
    let res;
    if (!Array.isArray(localInputs.models)) {
      showError(t('提交失败，请勿重复提交！'));
      handleCancel();
      return;
    }
    localInputs.auto_ban = autoBan ? 1 : 0;
    localInputs.models = localInputs.models.join(',');
    localInputs.group = localInputs.groups.join(',');
    if (isEdit) {
      res = await API.put(`/api/channel/`, {
        ...localInputs,
        id: parseInt(channelId),
      });
    } else {
      res = await API.post(`/api/channel/`, localInputs);
    }
    const { success, message } = res.data;
    if (success) {
      if (isEdit) {
        showSuccess(t('渠道更新成功！'));
      } else {
        showSuccess(t('渠道创建成功！'));
        setInputs(originInputs);
      }
      props.refresh();
      props.handleClose();
    } else {
      showError(message);
    }
  };

  const addCustomModels = () => {
    if (customModel.trim() === '') return;
    const modelArray = customModel.split(',').map((model) => model.trim());

    let localModels = [...inputs.models];
    let localModelOptions = [...modelOptions];
    let hasError = false;

    modelArray.forEach((model) => {
      if (model && !localModels.includes(model)) {
        localModels.push(model);
        localModelOptions.push({
          key: model,
          text: model,
          value: model,
        });
      } else if (model) {
        showError(t('某些模型已存在！'));
        hasError = true;
      }
    });

    if (hasError) return;

    setModelOptions(localModelOptions);
    setCustomModel('');
    handleInputChange('models', localModels);
  };

  return (
    <>
      <SideSheet
        maskClosable={false}
        placement={isEdit ? 'right' : 'left'}
        title={
          <Title level={3}>
            {isEdit ? t('更新渠道信息') : t('创建新的渠道')}
          </Title>
        }
        headerStyle={{ borderBottom: '1px solid var(--semi-color-border)' }}
        bodyStyle={{
          borderBottom: '1px solid var(--semi-color-border)',
          paddingBottom: '20px',
        }}
        visible={props.visible}
        footer={
          <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
            <Space>
              <Button theme='solid' size={'large'} onClick={submit}>
                {t('提交')}
              </Button>
              <Button
                theme='solid'
                size={'large'}
                type={'tertiary'}
                onClick={handleCancel}
              >
                {t('取消')}
              </Button>
            </Space>
          </div>
        }
        closeIcon={null}
        onCancel={() => handleCancel()}
        width={isMobile() ? '100%' : 600}
      >
        <Spin spinning={loading}>
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('类型')}：</Typography.Text>
          </div>
          <Select
            name='type'
            required
            optionList={CHANNEL_OPTIONS}
            value={inputs.type}
            onChange={(value) => handleInputChange('type', value)}
            style={{ width: '50%' }}
          />
          {inputs.type === 3 && (
            <>
              <div style={{ marginTop: 10 }}>
                <Banner
                  type={'warning'}
                  description={t(
                    '注意，模型部署名称必须和模型名称保持一致，因为 One API 会把请求体中的 model 参数替换为你的部署名称（模型名称中的点会被剔除）',
                  )}
                ></Banner>
              </div>
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>
                  AZURE_OPENAI_ENDPOINT：
                </Typography.Text>
              </div>
              <Input
                label='AZURE_OPENAI_ENDPOINT'
                name='azure_base_url'
                placeholder={t(
                  '请输入 AZURE_OPENAI_ENDPOINT，例如：https://docs-test-001.openai.azure.com',
                )}
                onChange={(value) => {
                  handleInputChange('base_url', value);
                }}
                value={inputs.base_url}
                autoComplete='new-password'
              />
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>{t('默认 API 版本')}：</Typography.Text>
              </div>
              <Input
                label={t('默认 API 版本')}
                name='azure_other'
                placeholder={t(
                  '请输入默认 API 版本，例如：2023-06-01-preview，该配置可以被实际的请求查询参数所覆盖',
                )}
                onChange={(value) => {
                  handleInputChange('other', value);
                }}
                value={inputs.other}
                autoComplete='new-password'
              />
            </>
          )}
          {inputs.type === 8 && (
            <>
              <div style={{ marginTop: 10 }}>
                <Banner
                  type={'warning'}
                  description='如果你对接的是上游One API或者New API等转发项目，请使用OpenAI类型，不要使用此类型，除非你知道你在做什么。'
                ></Banner>
              </div>
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>
                  {t('Base URL，支持变量{model}')}：
                </Typography.Text>
              </div>
              <Input
                name='base_url'
                placeholder='请输入完整的URL，例如：https://api.openai.com/v1/chat/completions'
                onChange={(value) => {
                  handleInputChange('base_url', value);
                }}
                value={inputs.base_url}
                autoComplete='new-password'
              />
            </>
          )}
          {inputs.type !== 3 &&
            inputs.type !== 8 &&
            inputs.type !== 22 &&
            inputs.type !== 36 && (
              <>
                {inputs.type === 44 && (
                  <>
                    <div style={{ marginTop: 10 }}>
                      <Typography.Text strong>
                        {t('Claude 认证方式')}：
                      </Typography.Text>
                    </div>
                    <Select
                      style={{ width: '50%' }}
                      value={getClaudeAuthMode()}
                      optionList={[
                        { label: t('秘钥（镜像站/自建）'), value: 'api_key' },
                        { label: t('Auth 登录（官方）'), value: 'oauth' },
                      ]}
                      onChange={(v) => {
                        applyClaudeAuthMode(v);
                      }}
                    />
                    <div style={{ marginTop: 10, display: 'flex' }}>
                      <Space>
                        <Checkbox
                          checked={getClaudeDeepSeekV4Mode()}
                          onChange={() => {
                            applyClaudeDeepSeekV4Mode(
                              !getClaudeDeepSeekV4Mode(),
                            );
                          }}
                        />
                        <Typography.Text strong>
                          适配 DeepSeek V4 官方渠道
                        </Typography.Text>
                      </Space>
                    </div>
                    <div style={{ marginTop: 10, display: 'flex' }}>
                      <Space>
                        <Checkbox
                          checked={getClaudeUseAnthropicBeta()}
                          onChange={() => {
                            applyClaudeUseAnthropicBeta(
                              !getClaudeUseAnthropicBeta(),
                            );
                          }}
                        />
                        <Typography.Text strong>
                          使用 anthropic-beta 请求头
                        </Typography.Text>
                      </Space>
                    </div>
                    <div style={{ marginTop: 10, display: 'flex' }}>
                      <Space>
                        <Checkbox
                          checked={getClaudeSimulateCLI()}
                          onChange={() => {
                            applyClaudeSimulateCLI(!getClaudeSimulateCLI());
                          }}
                        />
                        <Typography.Text strong>
                          完全模拟 Claude Code CLI 请求
                        </Typography.Text>
                      </Space>
                    </div>
                    {getClaudeAuthMode() === 'oauth' && (
                      <div style={{ marginTop: 10 }}>
                        <Banner
                          type='info'
                          style={{ marginBottom: 10 }}
                          description={t(
                            '线上部署无法接收 localhost 回调：登录授权完成后浏览器会跳转到 localhost（报错无影响），复制地址栏的回调 URL（包含 code 和 state）粘贴到下方再点击「完成授权」',
                          )}
                        />
                        <Space>
                          <Button
                            type='primary'
                            loading={claudeAuthLoading}
                            onClick={startClaudeOAuth}
                          >
                            {isEdit ? t('授权登录并绑定') : t('开始授权登录')}
                          </Button>
                          <Button
                            loading={claudeAuthLoading}
                            disabled={!claudeCallbackUrl.trim()}
                            onClick={completeClaudeOAuth}
                          >
                            {t('完成授权')}
                          </Button>
                          <Button
                            loading={claudeAuthLoading}
                            onClick={refreshClaudeAuthStatus}
                          >
                            {t('刷新状态')}
                          </Button>
                        </Space>
                        <div style={{ marginTop: 10 }}>
                          <Typography.Text strong>
                            {t('回调 URL')}：
                          </Typography.Text>
                        </div>
                        <TextArea
                          placeholder='http://localhost:54545/callback?code=...&state=...'
                          autosize={{ minRows: 2, maxRows: 4 }}
                          value={claudeCallbackUrl}
                          onChange={(v) => setClaudeCallbackUrl(v)}
                        />
                        {claudeAuthStatus && (
                          <Banner
                            style={{ marginTop: 10 }}
                            type='success'
                            description={
                              <>
                                <div>{t('授权信息已获取')}</div>
                                {claudeAuthStatus.email && (
                                  <div>
                                    {t('邮箱')}: {claudeAuthStatus.email}
                                  </div>
                                )}
                              </>
                            }
                          />
                        )}
                      </div>
                    )}
                  </>
                )}
                {inputs.type === 45 && (
                  <>
                    <div style={{ marginTop: 10 }}>
                      <Typography.Text strong>
                        {t('Codex 认证方式')}：
                      </Typography.Text>
                    </div>
                    <Select
                      style={{ width: '50%' }}
                      value={getCodexAuthMode()}
                      optionList={[
                        { label: t('秘钥（镜像站/自建）'), value: 'api_key' },
                        { label: t('Auth 登录（官方）'), value: 'oauth' },
                      ]}
                      onChange={(v) => {
                        applyCodexAuthMode(v);
                      }}
                    />
                    {getCodexAuthMode() === 'oauth' && (
                      <div style={{ marginTop: 10 }}>
                        <Banner
                          type='warning'
                          style={{ marginBottom: 10 }}
                          description={t(
                            '如遇到 “Country, region, or territory not supported”，请在「代理URL」填写可用地区的代理后再授权',
                          )}
                        />
                        <Banner
                          type='info'
                          style={{ marginBottom: 10 }}
                          description={t(
                            '线上部署无法接收 localhost 回调：登录授权完成后浏览器会跳转到 localhost（报错无影响），复制地址栏的回调 URL（包含 code 和 state）粘贴到下方再点击「完成授权」',
                          )}
                        />
                        <Space>
                          <Button
                            type='primary'
                            loading={codexAuthLoading}
                            onClick={startCodexOAuth}
                          >
                            {isEdit ? t('授权登录并绑定') : t('开始授权登录')}
                          </Button>
                          <Button
                            loading={codexAuthLoading}
                            disabled={!codexCallbackUrl.trim()}
                            onClick={completeCodexOAuth}
                          >
                            {t('完成授权')}
                          </Button>
                          <Button
                            loading={codexAuthLoading}
                            onClick={refreshCodexAuthStatus}
                          >
                            {t('刷新状态')}
                          </Button>
                        </Space>
                        <div style={{ marginTop: 10 }}>
                          <Typography.Text strong>
                            {t('回调 URL')}：
                          </Typography.Text>
                        </div>
                        <TextArea
                          placeholder='http://localhost:1455/auth/callback?code=...&state=...'
                          autosize={{ minRows: 2, maxRows: 4 }}
                          value={codexCallbackUrl}
                          onChange={(v) => setCodexCallbackUrl(v)}
                        />
                        {codexAuthStatus && (
                          <Banner
                            style={{ marginTop: 10 }}
                            type='success'
                            description={
                              <>
                                <div>{t('授权信息已获取')}</div>
                                {codexAuthStatus.email && (
                                  <div>
                                    {t('邮箱')}: {codexAuthStatus.email}
                                  </div>
                                )}
                                {codexAuthStatus.account_id && (
                                  <div>
                                    {t('AccountId')}:{' '}
                                    {codexAuthStatus.account_id}
                                  </div>
                                )}
                              </>
                            }
                          />
                        )}
                      </div>
                    )}
                  </>
                )}
                <div style={{ marginTop: 10 }}>
                  <Typography.Text strong>{t('请求地址')}：</Typography.Text>
                </div>
                <Input
                  label={t('请求地址')}
                  name='base_url'
                  placeholder={
                    (inputs.type === 45 && getCodexAuthMode() === 'oauth') ||
                    (inputs.type === 44 && getClaudeAuthMode() === 'oauth')
                      ? t('已自动填写官方请求地址')
                      : inputs.type === 45
                        ? '填入 Codex 镜像站接口地址, 通常以 /v1 结尾'
                        : inputs.type === 44
                          ? '填入 Claude Code 镜像站接口地址, 通常以 /v1 结尾'
                          : t('此项可选，用于通过代理站来进行 API 调用')
                  }
                  disabled={
                    (inputs.type === 45 && getCodexAuthMode() === 'oauth') ||
                    (inputs.type === 44 && getClaudeAuthMode() === 'oauth')
                  }
                  onChange={(value) => {
                    handleInputChange('base_url', value);
                  }}
                  value={inputs.base_url}
                  autoComplete='new-password'
                />
              </>
            )}
          {inputs.type === 22 && (
            <>
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>{t('私有部署地址')}：</Typography.Text>
              </div>
              <Input
                name='base_url'
                placeholder={t(
                  '请输入私有部署地址，格式为：https://fastgpt.run/api/openapi',
                )}
                onChange={(value) => {
                  handleInputChange('base_url', value);
                }}
                value={inputs.base_url}
                autoComplete='new-password'
              />
            </>
          )}
          {inputs.type === 36 && (
            <>
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>
                  {t(
                    '注意非Chat API，请务必填写正确的API地址，否则可能导致无法使用',
                  )}
                </Typography.Text>
              </div>
              <Input
                name='base_url'
                placeholder={t(
                  '请输入到 /suno 前的路径，通常就是域名，例如：https://api.example.com',
                )}
                onChange={(value) => {
                  handleInputChange('base_url', value);
                }}
                value={inputs.base_url}
                autoComplete='new-password'
              />
            </>
          )}
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('名称')}：</Typography.Text>
          </div>
          <Input
            required
            name='name'
            placeholder={t('请为渠道命名')}
            onChange={(value) => {
              handleInputChange('name', value);
            }}
            value={inputs.name}
            autoComplete='new-password'
          />
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('分组')}：</Typography.Text>
          </div>
          <Select
            placeholder={t('请选择可以使用该渠道的分组')}
            name='groups'
            required
            multiple
            selection
            allowAdditions
            additionLabel={t('请在系统设置页面编辑分组倍率以添加新的分组：')}
            onChange={(value) => {
              handleInputChange('groups', value);
            }}
            value={inputs.groups}
            autoComplete='new-password'
            optionList={groupOptions}
          />
          {inputs.type === 18 && (
            <>
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>模型版本：</Typography.Text>
              </div>
              <Input
                name='other'
                placeholder={
                  '请输入星火大模型版本，注意是接口地址中的版本号，例如：v2.1'
                }
                onChange={(value) => {
                  handleInputChange('other', value);
                }}
                value={inputs.other}
                autoComplete='new-password'
              />
            </>
          )}
          {inputs.type === 41 && (
            <>
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>{t('部署地区')}：</Typography.Text>
              </div>
              <TextArea
                name='other'
                placeholder={t(
                  '请输入部署地区，例如：us-central1\n支持使用模型映射格式\n' +
                    '{\n' +
                    '    "default": "us-central1",\n' +
                    '    "claude-3-5-sonnet-20240620": "europe-west1"\n' +
                    '}',
                )}
                autosize={{ minRows: 2 }}
                onChange={(value) => {
                  handleInputChange('other', value);
                }}
                value={inputs.other}
                autoComplete='new-password'
              />
              <Typography.Text
                style={{
                  color: 'rgba(var(--semi-blue-5), 1)',
                  userSelect: 'none',
                  cursor: 'pointer',
                }}
                onClick={() => {
                  handleInputChange(
                    'other',
                    JSON.stringify(REGION_EXAMPLE, null, 2),
                  );
                }}
              >
                {t('填入模板')}
              </Typography.Text>
            </>
          )}
          {inputs.type === 21 && (
            <>
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>��识库 ID：</Typography.Text>
              </div>
              <Input
                label='知识库 ID'
                name='other'
                placeholder={'请输入知识库 ID，例如：123456'}
                onChange={(value) => {
                  handleInputChange('other', value);
                }}
                value={inputs.other}
                autoComplete='new-password'
              />
            </>
          )}
          {inputs.type === 39 && (
            <>
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>Account ID：</Typography.Text>
              </div>
              <Input
                name='other'
                placeholder={
                  '请输入Account ID，例如：d6b5da8hk1awo8nap34ube6gh'
                }
                onChange={(value) => {
                  handleInputChange('other', value);
                }}
                value={inputs.other}
                autoComplete='new-password'
              />
            </>
          )}
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('模型')}：</Typography.Text>
          </div>
          <Select
            placeholder={'请选择该渠道所支持的模型'}
            name='models'
            required
            multiple
            selection
            filter
            searchPosition='dropdown'
            onChange={(value) => {
              handleInputChange('models', value);
            }}
            value={inputs.models}
            autoComplete='new-password'
            optionList={modelOptions}
          />
          <div style={{ lineHeight: '40px', marginBottom: '12px' }}>
            <Space>
              <Button
                type='primary'
                onClick={() => {
                  handleInputChange('models', basicModels);
                }}
              >
                {t('填入相关模型')}
              </Button>
              <Button
                type='secondary'
                onClick={() => {
                  handleInputChange('models', fullModels);
                }}
              >
                {t('填入所有模型')}
              </Button>
              <Tooltip
                content={t(
                  '新建渠道时，请求通过当前浏览器发出；编辑已有渠道，请求通过后端服务器发出',
                )}
              >
                <Button
                  type='tertiary'
                  onClick={() => {
                    fetchUpstreamModelList('models');
                  }}
                >
                  {t('获取模型列表')}
                </Button>
              </Tooltip>
              <Button
                type='warning'
                onClick={() => {
                  handleInputChange('models', []);
                }}
              >
                {t('清除所有模型')}
              </Button>
            </Space>
            <Input
              addonAfter={
                <Button type='primary' onClick={addCustomModels}>
                  {t('填入')}
                </Button>
              }
              placeholder={t('输入自定义模型名称')}
              value={customModel}
              onChange={(value) => {
                setCustomModel(value.trim());
              }}
            />
          </div>
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('模型重定向')}：</Typography.Text>
          </div>
          {/* 以行的形式编辑映射 */}
          <div
            style={{
              border: '1px solid var(--semi-color-border)',
              borderRadius: 6,
              padding: 12,
              background: 'var(--semi-color-bg-0)',
            }}
          >
            <div
              style={{
                maxHeight: 300,
                overflowY: 'auto',
                marginBottom: 8,
                paddingRight: 4,
              }}
            >
              {(modelMappingRows || []).length === 0 && (
                <Typography.Text type='tertiary'>
                  {t('未添加规则，点击下方“新增一条”开始')}。
                </Typography.Text>
              )}
              {(modelMappingRows || []).map((row, idx) => (
                <div
                  key={idx}
                  style={{ display: 'flex', gap: 8, marginBottom: 8 }}
                >
                  <Input
                    style={{ flex: 1 }}
                    placeholder={t('实际请求的模型名')}
                    value={row.from}
                    onChange={(v) => changeRow(idx, 'from', v)}
                    autoComplete='off'
                  />
                  <Typography.Text style={{ lineHeight: '32px' }}>
                    →
                  </Typography.Text>
                  <Input
                    style={{ flex: 1 }}
                    placeholder={t('重定向为真实可用的模型名')}
                    value={row.to}
                    onChange={(v) => changeRow(idx, 'to', v)}
                    autoComplete='off'
                  />
                  <Button type='danger' onClick={() => removeMappingRow(idx)}>
                    {t('删除')}
                  </Button>
                </div>
              ))}
            </div>
            <Space>
              <Button type='primary' onClick={addMappingRow}>
                {t('新增一条')}
              </Button>
              <Button type='warning' onClick={() => updateRows([])}>
                {t('清空')}
              </Button>
              <Button
                onClick={() => {
                  const rows = Object.entries(MODEL_MAPPING_EXAMPLE).map(
                    ([from, to]) => ({ from, to }),
                  );
                  updateRows(rows);
                }}
              >
                {t('填入模板')}
              </Button>
            </Space>
          </div>
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('密钥')}：</Typography.Text>
          </div>
          {(inputs.type === 45 && getCodexAuthMode() === 'oauth') ||
          (inputs.type === 44 && getClaudeAuthMode() === 'oauth') ? (
            <Input
              label={t('密钥')}
              name='key'
              disabled
              placeholder={t('Auth 模式无需填写密钥，授权后自动保存')}
              value=''
              autoComplete='new-password'
            />
          ) : batch ? (
            <TextArea
              label={t('密钥')}
              name='key'
              required
              placeholder={t('请输入密钥，一行一个')}
              onChange={(value) => {
                handleInputChange('key', value);
              }}
              value={inputs.key}
              style={{ minHeight: 150, fontFamily: 'JetBrains Mono, Consolas' }}
              autoComplete='new-password'
            />
          ) : (
            <>
              {inputs.type === 41 ? (
                <TextArea
                  label={t('鉴权json')}
                  name='key'
                  required
                  placeholder={
                    '{\n' +
                    '  "type": "service_account",\n' +
                    '  "project_id": "abc-bcd-123-456",\n' +
                    '  "private_key_id": "123xxxxx456",\n' +
                    '  "private_key": "-----BEGIN PRIVATE KEY-----xxxx\n' +
                    '  "client_email": "xxx@developer.gserviceaccount.com",\n' +
                    '  "client_id": "111222333",\n' +
                    '  "auth_uri": "https://accounts.google.com/o/oauth2/auth",\n' +
                    '  "token_uri": "https://oauth2.googleapis.com/token",\n' +
                    '  "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",\n' +
                    '  "client_x509_cert_url": "https://xxxxx.gserviceaccount.com",\n' +
                    '  "universe_domain": "googleapis.com"\n' +
                    '}'
                  }
                  onChange={(value) => {
                    handleInputChange('key', value);
                  }}
                  autosize={{ minRows: 10 }}
                  value={inputs.key}
                  autoComplete='new-password'
                />
              ) : (
                <Input
                  label={t('密钥')}
                  name='key'
                  required
                  placeholder={t(type2secretPrompt(inputs.type))}
                  onChange={(value) => {
                    handleInputChange('key', value);
                  }}
                  value={inputs.key}
                  autoComplete='new-password'
                />
              )}
            </>
          )}
          {!isEdit && (
            <div style={{ marginTop: 10, display: 'flex' }}>
              <Space>
                <Checkbox
                  checked={batch}
                  label={t('批量创建')}
                  name='batch'
                  disabled={
                    (inputs.type === 45 && getCodexAuthMode() === 'oauth') ||
                    (inputs.type === 44 && getClaudeAuthMode() === 'oauth')
                  }
                  onChange={() => setBatch(!batch)}
                />
                <Typography.Text strong>{t('批量创建')}</Typography.Text>
              </Space>
            </div>
          )}
          {inputs.type === 1 && (
            <>
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>{t('组织')}：</Typography.Text>
              </div>
              <Input
                label={t('组织，可选，不填则为默认组织')}
                name='openai_organization'
                placeholder={t('请输入组织org-xxx')}
                onChange={(value) => {
                  handleInputChange('openai_organization', value);
                }}
                value={inputs.openai_organization}
              />
            </>
          )}
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('默认测试模型')}：</Typography.Text>
          </div>
          <Input
            name='test_model'
            placeholder={t('不填则为模型列表第一个')}
            onChange={(value) => {
              handleInputChange('test_model', value);
            }}
            value={inputs.test_model}
          />
          <div style={{ marginTop: 10, display: 'flex' }}>
            <Space>
              <Checkbox
                name='auto_ban'
                checked={autoBan}
                onChange={() => {
                  setAutoBan(!autoBan);
                }}
              />
              <Typography.Text strong>
                {t(
                  '是否自动禁用（仅当自动禁用开启时有效），关闭后不会自动禁用该渠道：',
                )}
              </Typography.Text>
            </Space>
          </div>
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>
              {t('状态码复写（仅影响本地判断，不修改返回到上游的状态码）')}：
            </Typography.Text>
          </div>
          <TextArea
            placeholder={
              t(
                '此项可选，用于复写返回的状态码，比如将claude渠道的400错误复写为500（用于重试），请勿滥用该功能，例如：',
              ) +
              '\n' +
              JSON.stringify(STATUS_CODE_MAPPING_EXAMPLE, null, 2)
            }
            name='status_code_mapping'
            onChange={(value) => {
              handleInputChange('status_code_mapping', value);
            }}
            autosize
            value={inputs.status_code_mapping}
            autoComplete='new-password'
          />
          <Typography.Text
            style={{
              color: 'rgba(var(--semi-blue-5), 1)',
              userSelect: 'none',
              cursor: 'pointer',
            }}
            onClick={() => {
              handleInputChange(
                'status_code_mapping',
                JSON.stringify(STATUS_CODE_MAPPING_EXAMPLE, null, 2),
              );
            }}
          >
            {t('填入模板')}
          </Typography.Text>
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('渠道标签')}</Typography.Text>
          </div>
          <Input
            label={t('渠道标签')}
            name='tag'
            placeholder={t('渠道标签')}
            onChange={(value) => {
              handleInputChange('tag', value);
            }}
            value={inputs.tag}
            autoComplete='new-password'
          />
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('渠道优先级')}</Typography.Text>
          </div>
          <Input
            label={t('渠道优先级')}
            name='priority'
            placeholder={t('渠道优先级')}
            onChange={(value) => {
              const number = parseInt(value);
              if (isNaN(number)) {
                handleInputChange('priority', value);
              } else {
                handleInputChange('priority', number);
              }
            }}
            value={inputs.priority}
            autoComplete='new-password'
          />
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('渠道权重')}</Typography.Text>
          </div>
          <Input
            label={t('渠道权重')}
            name='weight'
            placeholder={t('渠道权重')}
            onChange={(value) => {
              const number = parseInt(value);
              if (isNaN(number)) {
                handleInputChange('weight', value);
              } else {
                handleInputChange('weight', number);
              }
            }}
            value={inputs.weight}
            autoComplete='new-password'
          />
          <div style={{ marginTop: 10 }}>
            <Typography.Text strong>{t('代理URL')}</Typography.Text>
          </div>
          <Input
            label={t('代理URL')}
            name='proxy_url'
            placeholder={t(
              '此项可选，用于设置HTTP代理，格式如：socks5://user:pass@host:port 或 http://proxy.example.com:8080',
            )}
            onChange={(value) => {
              handleInputChange('proxy_url', value);
            }}
            value={inputs.proxy_url}
            autoComplete='new-password'
          />
          {(inputs.type === 8 || inputs.type === 20 || inputs.type === 45) && (
            <>
              <div style={{ marginTop: 10 }}>
                <Typography.Text strong>{t('渠道额外设置')}：</Typography.Text>
              </div>
              <TextArea
                placeholder={
                  t(
                    '此项可选，用于配置渠道特定设置，为一个 JSON 字符串，例如：',
                  ) + '\n{\n  "force_format": true\n}'
                }
                name='setting'
                onChange={(value) => {
                  handleInputChange('setting', value);
                }}
                autosize
                value={inputs.setting}
                autoComplete='new-password'
              />
              <Typography.Text
                style={{
                  color: 'rgba(var(--semi-blue-5), 1)',
                  userSelect: 'none',
                  cursor: 'pointer',
                }}
                onClick={() => {
                  handleInputChange(
                    'setting',
                    JSON.stringify(
                      inputs.type === 45
                        ? { chatgpt_account_id: 'your_account_id' }
                        : inputs.type === 20
                          ? {
                              provider: {
                                order: ['Z.AI'],
                                allow_fallbacks: false,
                              },
                            }
                          : { force_format: true },
                      null,
                      2,
                    ),
                  );
                }}
              >
                {t('填入模板')}
              </Typography.Text>
            </>
          )}
        </Spin>
      </SideSheet>
    </>
  );
};

export default EditChannel;
