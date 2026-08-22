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

import React, { useEffect, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';
import CardPro from '../../common/ui/CardPro';
import UsersTable from './UsersTable';
import UsersActions from './UsersActions';
import UsersFilters from './UsersFilters';
import UsersDescription from './UsersDescription';
import AddUserModal from './modals/AddUserModal';
import EditUserModal from './modals/EditUserModal';
import { useUsersData } from '../../../hooks/users/useUsersData';
import { useIsMobile } from '../../../hooks/common/useIsMobile';
import { createCardProPagination } from '../../../helpers/utils';

const UsersPage = () => {
  const usersData = useUsersData();
  const isMobile = useIsMobile();
  const [searchParams, setSearchParams] = useSearchParams();
  const appliedKeywordRef = useRef(null);

  // URL ?keyword=xxx → 自动填入搜索框并搜索（用于从其它页面跳转过来定位）
  useEffect(() => {
    const kw = searchParams.get('keyword');
    if (!kw) return;
    if (!usersData.formApi) return;
    if (appliedKeywordRef.current === kw) return;
    appliedKeywordRef.current = kw;
    usersData.formApi.setValue('searchKeyword', kw);
    usersData.searchUsers(1, usersData.pageSize, kw, '');
    // 应用一次后清掉 URL 参数，避免再次刷新仍触发跳转效果
    const next = new URLSearchParams(searchParams);
    next.delete('keyword');
    setSearchParams(next, { replace: true });
  }, [searchParams, usersData.formApi]);

  const {
    // Modal state
    showAddUser,
    showEditUser,
    editingUser,
    setShowAddUser,
    closeAddUser,
    closeEditUser,
    refresh,

    // Form state
    formInitValues,
    setFormApi,
    searchUsers,
    loadUsers,
    activePage,
    pageSize,
    groupOptions,
    loading,
    searching,

    // Description state
    compactMode,
    setCompactMode,

    // Translation
    t,
  } = usersData;

  return (
    <>
      <AddUserModal
        refresh={refresh}
        visible={showAddUser}
        handleClose={closeAddUser}
      />

      <EditUserModal
        refresh={refresh}
        visible={showEditUser}
        handleClose={closeEditUser}
        editingUser={editingUser}
      />

      <CardPro
        type='type1'
        descriptionArea={
          <UsersDescription
            compactMode={compactMode}
            setCompactMode={setCompactMode}
            t={t}
          />
        }
        actionsArea={
          <div className='flex flex-col md:flex-row justify-between items-center gap-2 w-full'>
            <UsersActions setShowAddUser={setShowAddUser} t={t} />

            <UsersFilters
              formInitValues={formInitValues}
              setFormApi={setFormApi}
              searchUsers={searchUsers}
              loadUsers={loadUsers}
              activePage={activePage}
              pageSize={pageSize}
              groupOptions={groupOptions}
              loading={loading}
              searching={searching}
              t={t}
            />
          </div>
        }
        paginationArea={createCardProPagination({
          currentPage: usersData.activePage,
          pageSize: usersData.pageSize,
          total: usersData.userCount,
          onPageChange: usersData.handlePageChange,
          onPageSizeChange: usersData.handlePageSizeChange,
          isMobile: isMobile,
          t: usersData.t,
        })}
        t={usersData.t}
      >
        <UsersTable {...usersData} />
      </CardPro>
    </>
  );
};

export default UsersPage;
