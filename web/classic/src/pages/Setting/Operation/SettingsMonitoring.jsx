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

import React, { useEffect, useState, useRef } from 'react';
import { Button, Col, Form, Row, Spin } from '@douyinfe/semi-ui';
import {
  compareObjects,
  API,
  showError,
  showSuccess,
  showWarning,
  parseHttpStatusCodeRules,
} from '../../../helpers';
import { useTranslation } from 'react-i18next';
import HttpStatusCodeRulesInput from '../../../components/settings/HttpStatusCodeRulesInput';

const healthNumberFields = [
  ['base_degrade_threshold', '首次降级连续错误数', 1, 1],
  ['level_step_threshold', '每级额外连续错误数', 1, 1],
  ['max_degrade_level', '最大降级级别', 1, 1, 20],
  ['min_weight_factor', '最低权重系数', 0.01, 0.01, 1],
  ['disable_threshold', '自动禁用连续错误数（0 为关闭）', 0, 1],
  ['upgrade_threshold', '单级恢复连续成功数', 1, 1],
  ['max_ttft_ms', '最大首字响应时间（毫秒，0 为关闭）', 0, 100],
  ['latency_degrade_base', '延迟首次降级次数', 1, 1],
  ['latency_degrade_step', '延迟每级额外次数', 1, 1],
  ['streak_window_sec', '连续计数窗口（秒）', 60, 10],
  ['rebounce_protection_minutes', '反弹保护窗口（分钟，0 为关闭）', 0, 1],
  ['rebounce_protection_threshold', '反弹锁定阈值', 0, 1],
  ['demote_cooldown_sec', '降级冷却时间（秒）', 0, 1],
  ['recovery_probe_minutes', '禁用渠道恢复探活间隔（分钟）', 1, 1],
  ['shadow_sample_rate', '影子采样率', 0, 0.001, 1],
  ['degrade_probe_min_level', '降级渠道探活最低级别', 1, 1],
  ['degrade_probe_minutes', '降级渠道探活间隔（分钟）', 1, 1],
  ['degrade_probe_count', '每次降级渠道探活次数', 1, 1],
];

const healthBooleanFields = [
  ['count_latency_as_error', '将 TTFT 超时计入错误连续数'],
  ['count_429_as_error', '将 429 计入错误连续数'],
  ['notify_on_degrade', '降级时发送通知'],
  ['notify_on_upgrade', '恢复时发送通知'],
  ['degrade_probe_enabled', '启用降级渠道探活恢复'],
];

const parseHealthTagsFromStorage = (raw) => {
  if (!raw || raw === 'null') return '';
  try {
    const tags = JSON.parse(raw);
    return Array.isArray(tags) ? tags.join(',') : raw;
  } catch {
    return raw;
  }
};

const healthTagsToStorage = (display) =>
  JSON.stringify(
    String(display || '')
      .split(',')
      .map((tag) => tag.trim())
      .filter(Boolean),
  );

export default function SettingsMonitoring(props) {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [inputs, setInputs] = useState({
    ChannelDisableThreshold: '',
    QuotaRemindThreshold: '',
    AutomaticDisableChannelEnabled: false,
    AutomaticEnableChannelEnabled: false,
    AutomaticDisableKeywords: '',
    AutomaticDisableStatusCodes: '401',
    AutomaticRetryStatusCodes:
      '100-199,300-399,401-407,409-499,500-503,505-523,525-599',
    'monitor_setting.auto_test_channel_enabled': false,
    'monitor_setting.auto_test_channel_minutes': 10,
    'channel_health_setting.enabled': false,
    'channel_health_setting.base_degrade_threshold': 5,
    'channel_health_setting.level_step_threshold': 5,
    'channel_health_setting.max_degrade_level': 10,
    'channel_health_setting.min_weight_factor': 0.05,
    'channel_health_setting.disable_threshold': 0,
    'channel_health_setting.upgrade_threshold': 20,
    'channel_health_setting.max_ttft_ms': 0,
    'channel_health_setting.latency_degrade_base': 5,
    'channel_health_setting.latency_degrade_step': 5,
    'channel_health_setting.count_latency_as_error': false,
    'channel_health_setting.count_429_as_error': true,
    'channel_health_setting.countable_status_codes': '',
    'channel_health_setting.notify_on_degrade': false,
    'channel_health_setting.notify_on_upgrade': false,
    'channel_health_setting.streak_window_sec': 600,
    'channel_health_setting.rebounce_protection_minutes': 0,
    'channel_health_setting.rebounce_protection_threshold': 3,
    'channel_health_setting.demote_cooldown_sec': 60,
    'channel_health_setting.recovery_strategy': 'probe',
    'channel_health_setting.recovery_probe_minutes': 30,
    'channel_health_setting.recovery_probe_model': '',
    'channel_health_setting.shadow_sample_rate': 0,
    'channel_health_setting.skip_channel_tags': '',
    'channel_health_setting.degrade_probe_enabled': true,
    'channel_health_setting.degrade_probe_min_level': 1,
    'channel_health_setting.degrade_probe_minutes': 10,
    'channel_health_setting.degrade_probe_count': 5,
  });
  const refForm = useRef();
  const [inputsRow, setInputsRow] = useState(inputs);
  const parsedAutoDisableStatusCodes = parseHttpStatusCodeRules(
    inputs.AutomaticDisableStatusCodes || '',
  );
  const parsedAutoRetryStatusCodes = parseHttpStatusCodeRules(
    inputs.AutomaticRetryStatusCodes || '',
  );
  const parsedChannelHealthStatusCodes = parseHttpStatusCodeRules(
    inputs['channel_health_setting.countable_status_codes'] || '',
  );

  function onSubmit() {
    const updateArray = compareObjects(inputs, inputsRow);
    if (!updateArray.length) return showWarning(t('你似乎并没有修改什么'));
    if (!parsedAutoDisableStatusCodes.ok) {
      const details =
        parsedAutoDisableStatusCodes.invalidTokens &&
        parsedAutoDisableStatusCodes.invalidTokens.length > 0
          ? `: ${parsedAutoDisableStatusCodes.invalidTokens.join(', ')}`
          : '';
      return showError(`${t('自动禁用状态码格式不正确')}${details}`);
    }
    if (!parsedAutoRetryStatusCodes.ok) {
      const details =
        parsedAutoRetryStatusCodes.invalidTokens &&
        parsedAutoRetryStatusCodes.invalidTokens.length > 0
          ? `: ${parsedAutoRetryStatusCodes.invalidTokens.join(', ')}`
          : '';
      return showError(`${t('自动重试状态码格式不正确')}${details}`);
    }
    if (!parsedChannelHealthStatusCodes.ok) {
      const details =
        parsedChannelHealthStatusCodes.invalidTokens?.length > 0
          ? `: ${parsedChannelHealthStatusCodes.invalidTokens.join(', ')}`
          : '';
      return showError(`${t('渠道健康状态码格式不正确')}${details}`);
    }
    const requestQueue = updateArray.map((item) => {
      let value = '';
      if (typeof inputs[item.key] === 'boolean') {
        value = String(inputs[item.key]);
      } else {
        const normalizedMap = {
          AutomaticDisableStatusCodes: parsedAutoDisableStatusCodes.normalized,
          AutomaticRetryStatusCodes: parsedAutoRetryStatusCodes.normalized,
          'channel_health_setting.countable_status_codes':
            parsedChannelHealthStatusCodes.normalized,
          'channel_health_setting.skip_channel_tags': healthTagsToStorage(
            inputs['channel_health_setting.skip_channel_tags'],
          ),
        };
        value = normalizedMap[item.key] ?? inputs[item.key];
      }
      return API.put('/api/option/', {
        key: item.key,
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

  useEffect(() => {
    const currentInputs = {};
    for (let key in props.options) {
      if (Object.keys(inputs).includes(key)) {
        currentInputs[key] =
          key === 'channel_health_setting.skip_channel_tags'
            ? parseHealthTagsFromStorage(props.options[key])
            : props.options[key];
      }
    }
    setInputs(currentInputs);
    setInputsRow(structuredClone(currentInputs));
    refForm.current.setValues(currentInputs);
  }, [props.options]);

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
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.Switch
                  field={'monitor_setting.auto_test_channel_enabled'}
                  label={t('定时测试所有通道')}
                  size='default'
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      'monitor_setting.auto_test_channel_enabled': value,
                    })
                  }
                />
              </Col>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  label={t('自动测试所有通道间隔时间')}
                  step={1}
                  min={1}
                  suffix={t('分钟')}
                  extraText={t('每隔多少分钟测试一次所有通道')}
                  placeholder={''}
                  field={'monitor_setting.auto_test_channel_minutes'}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      'monitor_setting.auto_test_channel_minutes':
                        parseInt(value),
                    })
                  }
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
                <Form.InputNumber
                  label={t('测试所有渠道的最长响应时间')}
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
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
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
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
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
              <Col xs={24} sm={12} md={8} lg={8} xl={8}>
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
            </Row>
            <Row gutter={16}>
              <Col xs={24} sm={16}>
                <HttpStatusCodeRulesInput
                  label={t('自动禁用状态码')}
                  placeholder={t('例如：401, 403, 429, 500-599')}
                  extraText={t(
                    '支持填写单个状态码或范围（含首尾），使用逗号分隔',
                  )}
                  field={'AutomaticDisableStatusCodes'}
                  onChange={(value) =>
                    setInputs({ ...inputs, AutomaticDisableStatusCodes: value })
                  }
                  parsed={parsedAutoDisableStatusCodes}
                  invalidText={t('自动禁用状态码格式不正确')}
                />
                <HttpStatusCodeRulesInput
                  label={t('自动重试状态码')}
                  placeholder={t('例如：401, 403, 429, 500-599')}
                  extraText={t(
                    '支持填写单个状态码或范围（含首尾），使用逗号分隔；504 和 524 始终不重试，不受此处配置影响',
                  )}
                  field={'AutomaticRetryStatusCodes'}
                  onChange={(value) =>
                    setInputs({ ...inputs, AutomaticRetryStatusCodes: value })
                  }
                  parsed={parsedAutoRetryStatusCodes}
                  invalidText={t('自动重试状态码格式不正确')}
                />
                <Form.TextArea
                  label={t('自动禁用关键词')}
                  placeholder={t('一行一个，不区分大小写')}
                  extraText={t(
                    '当上游通道返回错误中包含这些关键词时（不区分大小写），自动禁用通道',
                  )}
                  field={'AutomaticDisableKeywords'}
                  autosize={{ minRows: 6, maxRows: 12 }}
                  onChange={(value) =>
                    setInputs({ ...inputs, AutomaticDisableKeywords: value })
                  }
                />
              </Col>
            </Row>
            <Row>
              <Button size='default' onClick={onSubmit}>
                {t('保存监控设置')}
              </Button>
            </Row>
          </Form.Section>
          <Form.Section text={t('渠道健康（被动降级与恢复）')}>
            <Row gutter={16}>
              <Col xs={24} sm={12}>
                <Form.Switch
                  field='channel_health_setting.enabled'
                  label={t('启用被动渠道健康管理')}
                  extraText={t(
                    '仅使用真实请求的成功、错误和延迟信号；与定时测试开关相互独立。',
                  )}
                  checkedText='｜'
                  uncheckedText='〇'
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      'channel_health_setting.enabled': value,
                    })
                  }
                />
              </Col>
              {healthBooleanFields.map(([key, label]) => {
                const field = `channel_health_setting.${key}`;
                return (
                  <Col xs={24} sm={12} md={8} key={field}>
                    <Form.Switch
                      field={field}
                      label={t(label)}
                      checkedText='｜'
                      uncheckedText='〇'
                      onChange={(value) =>
                        setInputs({ ...inputs, [field]: value })
                      }
                    />
                  </Col>
                );
              })}
            </Row>
            <Row gutter={16}>
              {healthNumberFields.map(([key, label, min, step, max]) => {
                const field = `channel_health_setting.${key}`;
                return (
                  <Col xs={24} sm={12} md={8} key={field}>
                    <Form.InputNumber
                      field={field}
                      label={t(label)}
                      min={min}
                      max={max}
                      step={step}
                      onChange={(value) =>
                        setInputs({ ...inputs, [field]: Number(value) })
                      }
                    />
                  </Col>
                );
              })}
            </Row>
            <Row gutter={16}>
              <Col xs={24} sm={12} md={8}>
                <Form.Select
                  field='channel_health_setting.recovery_strategy'
                  label={t('禁用渠道恢复策略')}
                  optionList={[
                    { value: 'probe', label: t('自动探活恢复') },
                    { value: 'manual', label: t('仅管理员手动恢复') },
                  ]}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      'channel_health_setting.recovery_strategy': value,
                    })
                  }
                />
              </Col>
              <Col xs={24} sm={12} md={8}>
                <Form.Input
                  field='channel_health_setting.recovery_probe_model'
                  label={t('恢复探活模型')}
                  extraText={t('留空时由后端选择渠道可用模型')}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      'channel_health_setting.recovery_probe_model': value,
                    })
                  }
                />
              </Col>
              <Col xs={24} sm={12} md={8}>
                <Form.Input
                  field='channel_health_setting.skip_channel_tags'
                  label={t('忽略的渠道标签')}
                  extraText={t('使用英文逗号分隔')}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      'channel_health_setting.skip_channel_tags': value,
                    })
                  }
                />
              </Col>
            </Row>
            <Row gutter={16}>
              <Col xs={24} sm={16}>
                <Form.Input
                  field='channel_health_setting.countable_status_codes'
                  label={t('计入连续错误的 HTTP 状态码')}
                  placeholder={t('例如：429,500-599；留空使用后端默认规则')}
                  onChange={(value) =>
                    setInputs({
                      ...inputs,
                      'channel_health_setting.countable_status_codes': value,
                    })
                  }
                />
              </Col>
            </Row>
            <Row>
              <Button size='default' onClick={onSubmit}>
                {t('保存渠道健康设置')}
              </Button>
            </Row>
          </Form.Section>
        </Form>
      </Spin>
    </>
  );
}
