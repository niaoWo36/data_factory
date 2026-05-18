/**
 * tenant.js – Step 2: tenant selection
 */

async function loadTenants() {
  const listEl = document.getElementById('tenantList');
  listEl.innerHTML = '<span class="text-muted">加载中…</span>';
  try {
    const data = await apiGet('/api/tenants');
    if (data.error) {
      listEl.innerHTML = `<div class="alert alert-danger mb-0 py-2">${data.error}</div>`;
      return;
    }
    const tenants = data.tenants || [];
    if (tenants.length === 0) {
      listEl.innerHTML = '<span class="text-muted">没有找到任何租户</span>';
      return;
    }
    listEl.innerHTML = tenants.map(t => `
      <div class="form-check">
        <input class="form-check-input tenant-cb" type="checkbox" value="${escHtml(t.id)}"
               id="t_${escHtml(t.id)}" checked />
        <label class="form-check-label" for="t_${escHtml(t.id)}">
          <strong>${escHtml(t.name)}</strong>
          <span class="text-muted small ms-2">${escHtml(t.id)}</span>
        </label>
      </div>`).join('');

    // Pre-select all
    window.AppState.tenantIDs = tenants.map(t => t.id);
  } catch (e) {
    listEl.innerHTML = `<div class="alert alert-danger mb-0 py-2">请求失败: ${e.message}</div>`;
  }
}

function escHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function getSelectedTenants() {
  return Array.from(document.querySelectorAll('.tenant-cb:checked')).map(cb => cb.value);
}

document.addEventListener('DOMContentLoaded', () => {
  document.getElementById('btnSelectAll').addEventListener('click', () => {
    document.querySelectorAll('.tenant-cb').forEach(cb => cb.checked = true);
  });

  document.getElementById('btnDeselectAll').addEventListener('click', () => {
    document.querySelectorAll('.tenant-cb').forEach(cb => cb.checked = false);
  });

  document.getElementById('btnReloadTenants').addEventListener('click', loadTenants);

  document.getElementById('btnTenantNext').addEventListener('click', () => {
    const selected = getSelectedTenants();
    if (selected.length === 0) {
      alert('请至少选择一个租户');
      return;
    }
    window.AppState.tenantIDs = selected;
    unlockStep(3);
    goToStep(3);
    syncExportTenantList();
  });
});
