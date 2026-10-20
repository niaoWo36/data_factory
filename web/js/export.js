/**
 * export.js – Step 5: SQL export
 */

// Sync tenant list from step-2 selection into the export panel.
function syncExportTenantList() {
  const container = document.getElementById('expTenantList');
  const tenantIDs = window.AppState.tenantIDs || [];
  if (tenantIDs.length === 0) {
    const sw = document.getElementById('exportOnlySwitch');
    const exportOnly = sw ? sw.checked : !!(window.AppState.config && window.AppState.config.export_only);
    container.innerHTML = exportOnly
      ? '<span class="text-muted small">未选择租户 → 导出全部数据</span>'
      : '<span class="text-muted small">请先完成第2步租户选择</span>';
    return;
  }

  // Show tenant IDs that were selected.
  container.innerHTML = tenantIDs.map(id => `
    <div class="form-check">
      <input class="form-check-input exp-tenant-cb" type="checkbox"
             value="${escHtml(id)}" id="exp_t_${escHtml(id)}" checked />
      <label class="form-check-label small" for="exp_t_${escHtml(id)}">${escHtml(id)}</label>
    </div>`).join('');
}

function getExportTenants() {
  return Array.from(document.querySelectorAll('.exp-tenant-cb:checked')).map(cb => cb.value);
}

// Show the export-only hint when the app runs without a target database.
function syncExportModeNote() {
  const note = document.getElementById('expModeNote');
  if (!note) return;
  const sw = document.getElementById('exportOnlySwitch');
  const cfg = window.AppState.config;
  const exportOnly = sw ? sw.checked : !!(cfg && cfg.export_only);
  note.classList.toggle('d-none', !exportOnly);
}

document.addEventListener('DOMContentLoaded', () => {
  syncExportModeNote();
  window.addEventListener('df:config-changed', syncExportModeNote);

  // Toggle tenant section visibility based on DDL-only option.
  document.querySelectorAll('input[name="expDataOpt"]').forEach(el => {
    el.addEventListener('change', () => {
      const show = document.getElementById('expDDLData').checked;
      document.getElementById('expTenantSection').style.display = show ? '' : 'none';
    });
  });

  document.getElementById('btnExport').addEventListener('click', async () => {
    let cfg = window.AppState.config;
    if (!cfg && typeof buildConfig === 'function') {
      // Not saved yet — use the values currently entered in step 1.
      cfg = buildConfig();
    }
    if (!cfg) { alert('请先完成连接配置'); goToStep(1); return; }

    const includeData = document.getElementById('expDDLData').checked;
    const tenantIDs   = includeData ? getExportTenants() : [];

    const opts = {
      config:       cfg,
      tenant_ids:   tenantIDs,
      include_main: document.getElementById('expMain').checked,
      include_ts:   document.getElementById('expTS').checked,
      include_data: includeData,
    };

    const btn = document.getElementById('btnExport');
    btn.disabled = true;
    btn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span>生成中…';
    document.getElementById('exportResult').innerHTML = '';

    try {
      const data = await apiPost('/api/export/sql', opts);
      if (data.error) {
        document.getElementById('exportResult').innerHTML =
          `<div class="alert alert-danger mt-2">${escHtml(data.error)}</div>`;
        return;
      }
      document.getElementById('exportResult').innerHTML = `
        <div class="alert alert-success mt-2 d-flex align-items-center gap-3">
          <i class="bi bi-check-circle-fill fs-4"></i>
          <div>
            <strong>导出成功！</strong><br />
            <a href="${data.download_url}" class="btn btn-sm btn-success mt-1" download>
              <i class="bi bi-download me-1"></i>下载 SQL 文件
            </a>
          </div>
        </div>`;
    } catch (e) {
      document.getElementById('exportResult').innerHTML =
        `<div class="alert alert-danger mt-2">请求失败: ${escHtml(e.message)}</div>`;
    } finally {
      btn.disabled = false;
      btn.innerHTML = '<i class="bi bi-download me-1"></i>生成并下载 SQL';
    }
  });
});
