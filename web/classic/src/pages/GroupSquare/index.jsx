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

import React, { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  Card,
  Empty,
  Spin,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconArrowRight } from '@douyinfe/semi-icons';
import { Shuffle } from 'lucide-react';
import {
  OpenAI,
  Claude,
  Gemini,
  DeepSeek,
  Qwen,
  XAI,
} from '@lobehub/icons';
import { API, showError } from '../../helpers';

const { Title, Text, Paragraph } = Typography;

const formatRatio = (ratio) => {
  const n = Number(ratio);
  if (Number.isNaN(n)) return ratio;
  if (n === Math.floor(n)) return `×${n}`;
  return `×${n.toFixed(2)}`;
};

const PLATFORM_ICON_SIZE = 20;

const PLATFORMS = [
  {
    key: 'auto',
    label: '智能路由',
    icon: <Shuffle size={PLATFORM_ICON_SIZE} strokeWidth={2} />,
    color: { bg: '#d1fae5', fg: '#047857' },
    match: (name) => /(^|\s)default\b|^auto$|智能/i.test(name),
  },
  {
    key: 'openai',
    label: 'OpenAI',
    icon: <OpenAI size={PLATFORM_ICON_SIZE} />,
    color: { bg: '#f4f4f5', fg: '#3f3f46' },
    match: (name) => /\bgpt\b|openai|codex|chatgpt|o1\b|o3\b|o4\b|azure/i.test(name),
  },
  {
    key: 'claude',
    label: 'Anthropic Claude',
    icon: <Claude.Color size={PLATFORM_ICON_SIZE} />,
    color: { bg: '#ffedd5', fg: '#c2410c' },
    match: (name) =>
      /claude|anthropic|sonnet|opus|haiku|kiro|anit|windsurf|cx2cc/i.test(name),
  },
  {
    key: 'gemini',
    label: 'Google Gemini',
    icon: <Gemini.Color size={PLATFORM_ICON_SIZE} />,
    color: { bg: '#dbeafe', fg: '#1d4ed8' },
    match: (name) => /gemini|google|bard|vertex|banana|香蕉/i.test(name),
  },
  {
    key: 'deepseek',
    label: 'DeepSeek',
    icon: <DeepSeek.Color size={PLATFORM_ICON_SIZE} />,
    color: { bg: '#e0e7ff', fg: '#4338ca' },
    match: (name) => /deepseek/i.test(name),
  },
  {
    key: 'xai',
    label: 'xAI Grok',
    icon: <XAI size={PLATFORM_ICON_SIZE} />,
    color: { bg: '#e5e5e5', fg: '#262626' },
    match: (name) => /\bxai\b|grok/i.test(name),
  },
  {
    key: 'cn',
    label: '国产模型',
    icon: <Qwen.Color size={PLATFORM_ICON_SIZE} />,
    color: { bg: '#ede9fe', fg: '#6d28d9' },
    match: (name) =>
      /made in china|国产|minimax|glm|chatglm|kimi|moonshot|qwen|tongyi|通义|doubao|豆包|hunyuan|混元|wenxin|文心|spark|讯飞|yi-|零一|01ai/i.test(
        name,
      ),
  },
];

const classify = (group) => {
  const haystack = `${group.name} ${group.desc || ''}`;
  for (const p of PLATFORMS) {
    if (p.match(haystack)) return p.key;
  }
  return 'other';
};

const GroupSquare = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(true);
  const [groups, setGroups] = useState([]);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const res = await API.get('/api/user/self/groups');
        const { success, message, data } = res.data || {};
        if (cancelled) return;
        if (success) {
          // 与 default 主题的 HIDDEN_GROUPS 同口径：default 是所有用户的兜底分组，
          // 卡片没有挑选价值，只在分组广场隐藏，Keys 选择器等仍可见可选。
          const list = Object.entries(data || {})
            .filter(([name]) => name !== 'default')
            .map(([name, info]) => ({
              name,
              ratio: info?.ratio,
              desc: info?.desc || '',
            }));
          setGroups(list);
        } else {
          showError(t(message || '加载分组失败'));
        }
      } catch (err) {
        if (!cancelled) showError(err?.message || 'Failed to load groups');
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    load();
    return () => {
      cancelled = true;
    };
  }, [t]);

  const sections = useMemo(() => {
    const buckets = new Map();
    for (const g of groups) {
      const k = classify(g);
      if (!buckets.has(k)) buckets.set(k, []);
      buckets.get(k).push(g);
    }
    const sortGroups = (a, b) => {
      const ra = Number(a.ratio);
      const rb = Number(b.ratio);
      if (!Number.isNaN(ra) && !Number.isNaN(rb) && ra !== rb) return ra - rb;
      return a.name.localeCompare(b.name);
    };
    const result = [];
    for (const p of PLATFORMS) {
      const list = buckets.get(p.key);
      if (list && list.length) {
        result.push({ ...p, groups: list.sort(sortGroups) });
      }
    }
    const otherList = buckets.get('other');
    if (otherList && otherList.length) {
      result.push({
        key: 'other',
        label: '其他',
        icon: null,
        color: { bg: '#f3f4f6', fg: '#6b7280' },
        groups: otherList.sort(sortGroups),
      });
    }
    return result;
  }, [groups, t]);

  const handlePick = (groupName) => {
    navigate(`/console/token?group=${encodeURIComponent(groupName)}`);
  };

  const renderCard = (g, color) => (
    <Card
      key={g.name}
      className='!rounded-2xl shadow-sm border-0 h-full flex flex-col'
      bodyStyle={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        padding: 16,
      }}
    >
      <div className='flex items-start justify-between gap-2 mb-2'>
        <Title heading={6} className='!m-0 flex-1 min-w-0' title={g.name}>
          <span className='block truncate'>{g.name}</span>
        </Title>
        <Tag color='blue' shape='circle' size='large' className='shrink-0'>
          {t('倍率')} {formatRatio(g.ratio)}
        </Tag>
      </div>
      <Paragraph
        className='flex-1 !mb-3 text-sm'
        ellipsis={{ rows: 4, showTooltip: true }}
        style={{ color: 'var(--semi-color-text-2)' }}
      >
        {g.desc || t('暂无说明')}
      </Paragraph>
      <div className='flex justify-end'>
        <button
          type='button'
          title={t('使用此分组创建令牌')}
          aria-label={t('使用此分组创建令牌')}
          onClick={() => handlePick(g.name)}
          className='flex items-center justify-center w-9 h-9 rounded-xl hover:opacity-80 transition-opacity border-0 cursor-pointer'
          style={{ backgroundColor: color.bg, color: color.fg }}
        >
          <IconArrowRight />
        </button>
      </div>
    </Card>
  );

  return (
    <div className='mt-[60px] px-2 pb-8'>
      <div className='mb-4 px-1'>
        <Title heading={3} className='!m-0'>
          {t('分组广场')}
        </Title>
        <Text type='tertiary' className='block mt-1'>
          {t('选择一个分组，一键创建对应令牌')}
        </Text>
      </div>

      <Spin spinning={loading}>
        {!loading && sections.length === 0 ? (
          <Empty title={t('暂无可用分组')} />
        ) : (
          sections.map((section) => (
            <div key={section.key} className='mb-6'>
              <div className='flex items-center gap-2 mb-3 px-1'>
                <div
                  title={t(section.label)}
                  className='flex items-center justify-center w-9 h-9 rounded-xl'
                  style={{ backgroundColor: section.color.bg, color: section.color.fg }}
                >
                  {section.icon || (
                    <span className='text-xs font-semibold'>·</span>
                  )}
                </div>
                <Tag size='small' color='grey' shape='circle'>
                  {section.groups.length}
                </Tag>
              </div>
              <div className='grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3'>
                {section.groups.map((g) => renderCard(g, section.color))}
              </div>
            </div>
          ))
        )}
      </Spin>
    </div>
  );
};

export default GroupSquare;
