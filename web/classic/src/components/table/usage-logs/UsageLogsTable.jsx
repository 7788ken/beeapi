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

import React, { useMemo, useState, useEffect } from 'react';
import { Empty, Descriptions } from '@douyinfe/semi-ui';
import { useNavigate } from 'react-router-dom';
import CardTable from '../../common/ui/CardTable';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { getLogsColumns } from './UsageLogsColumnDefs';

const LogsTable = (logsData) => {
  const {
    logs,
    expandData,
    loading,
    activePage,
    pageSize,
    logCount,
    compactMode,
    visibleColumns,
    handlePageChange,
    handlePageSizeChange,
    copyText,
    showUserInfoFunc,
    openChannelAffinityUsageCacheModal,
    hasExpandableRows,
    isAdminUser,
    billingDisplayMode,
    t,
    COLUMN_KEYS,
  } = logsData;

  const navigate = useNavigate();

  // 受控展开：用 onDoubleClick 控制；保留小箭头点击也工作
  const [expandedRowKeys, setExpandedRowKeys] = useState([]);

  // 数据变化时清掉已不存在的 key，避免幽灵展开
  useEffect(() => {
    if (expandedRowKeys.length === 0) return;
    const validKeys = new Set((logs || []).map((row) => row.key));
    setExpandedRowKeys((prev) => prev.filter((k) => validKeys.has(k)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [logs]);

  const isRowExpandable = (record) =>
    expandData[record.key] && expandData[record.key].length > 0;

  // Get all columns
  const allColumns = useMemo(() => {
    return getLogsColumns({
      t,
      COLUMN_KEYS,
      copyText,
      showUserInfoFunc,
      openChannelAffinityUsageCacheModal,
      isAdminUser,
      billingDisplayMode,
      navigate,
    });
  }, [
    t,
    COLUMN_KEYS,
    copyText,
    showUserInfoFunc,
    openChannelAffinityUsageCacheModal,
    isAdminUser,
    billingDisplayMode,
    navigate,
  ]);

  // Filter columns based on visibility settings
  const getVisibleColumns = () => {
    return allColumns.filter((column) => visibleColumns[column.key]);
  };

  const visibleColumnsList = useMemo(() => {
    return getVisibleColumns();
  }, [visibleColumns, allColumns]);

  const tableColumns = useMemo(() => {
    return compactMode
      ? visibleColumnsList.map(({ fixed, ...rest }) => rest)
      : visibleColumnsList;
  }, [compactMode, visibleColumnsList]);

  const expandRowRender = (record, index) => {
    const items = expandData[record.key] || [];
    const handleValueClick = (e) => {
      // 内部交互元素让原行为优先（链接、按钮、输入、选择）
      const tag = e.target?.tagName;
      if (
        tag === 'A' ||
        tag === 'BUTTON' ||
        tag === 'INPUT' ||
        tag === 'SELECT' ||
        tag === 'TEXTAREA'
      ) {
        return;
      }
      const text = (
        e.currentTarget.innerText ||
        e.currentTarget.textContent ||
        ''
      ).trim();
      if (!text) return;
      if (typeof copyText === 'function') {
        // copyText 签名: (event, text) — event 用于 stopPropagation，text 是要复制的内容
        copyText(e, text);
      }
    };
    const wrappedItems = items.map((item, idx) => ({
      ...item,
      value: (
        <span
          key={idx}
          onClick={handleValueClick}
          style={{ cursor: 'pointer' }}
          title={t('单击复制')}
        >
          {item.value}
        </span>
      ),
    }));
    return <Descriptions data={wrappedItems} />;
  };

  return (
    <CardTable
      columns={tableColumns}
      {...(hasExpandableRows() && {
        expandedRowRender: expandRowRender,
        rowExpandable: isRowExpandable,
        expandedRowKeys,
        onExpandedRowsChange: setExpandedRowKeys,
      })}
      onRow={(record) => ({
        onDoubleClick: (event) => {
          // 列内自定义 onDoubleClick 已 stopPropagation，所以这里只处理空白处双击
          if (!isRowExpandable(record)) return;
          const key = record.key;
          setExpandedRowKeys((prev) =>
            prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]
          );
        },
      })}
      dataSource={logs}
      rowKey='key'
      loading={loading}
      scroll={compactMode ? undefined : { x: 'max-content' }}
      className='rounded-xl overflow-hidden'
      size='small'
      empty={
        <Empty
          image={<IllustrationNoResult style={{ width: 150, height: 150 }} />}
          darkModeImage={
            <IllustrationNoResultDark style={{ width: 150, height: 150 }} />
          }
          description={t('搜索无结果')}
          style={{ padding: 30 }}
        />
      }
      pagination={{
        currentPage: activePage,
        pageSize: pageSize,
        total: logCount,
        pageSizeOptions: [10, 20, 50, 100],
        showSizeChanger: true,
        onPageSizeChange: (size) => {
          handlePageSizeChange(size);
        },
        onPageChange: handlePageChange,
      }}
      hidePagination={true}
    />
  );
};

export default LogsTable;
