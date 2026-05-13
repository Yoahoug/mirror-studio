import { FormEvent, ReactNode, useEffect, useMemo, useState } from "react";
import {
  BadgeCheck,
  Bot,
  Image as ImageIcon,
  Layers3,
  Loader2,
  LogOut,
  RefreshCw,
  Settings2,
  Sparkles,
  Users,
  Wand2,
} from "lucide-react";
import {
  batchUpdateConfig,
  createImageChannel,
  deleteImageChannel,
  fetchConfig,
  fetchDashboard,
  fetchImageChannels,
  fetchTasks,
  fetchUserDetail,
  fetchUsers,
  logout,
  updateConfig,
  updateImageChannel,
  updateUserQuota,
  type AdminTask,
  type AdminUser,
  type AdminUserDetail,
  type ConfigListResponse,
  type DashboardData,
  type FailureLog,
  type ImageChannel,
  type ImageChannelPayload,
  type Pagination,
  type SiteConfigItem,
} from "./services/apiService";
import { ToastContainer, type ToastType } from "./components/Toast";

type TabId = "dashboard" | "users" | "tasks" | "channels" | "settings";

type UserQuotaModalState = {
  open: boolean;
  user: AdminUserDetail | null;
};

type ChannelModalState = {
  open: boolean;
  item: ImageChannel | null;
};

const tabs: Array<{ id: TabId; label: string; desc: string; icon: typeof Layers3 }> = [
  { id: "dashboard", label: "运营概览", desc: "查看账号、任务与图片统计", icon: Layers3 },
  { id: "users", label: "用户管理", desc: "管理账号与每日免费次数", icon: Users },
  { id: "tasks", label: "任务管理", desc: "查看生图任务状态与耗时", icon: Wand2 },
  { id: "channels", label: "渠道配置", desc: "维护生图与 LLM 对接渠道", icon: Bot },
  { id: "settings", label: "系统设置", desc: "维护站点与默认次数配置", icon: Settings2 },
];

const trackedSettingKeys = [
  "site.default_daily_free_quota",
  "site.register_enabled",
  "site.login_captcha_enabled",
  "site.daily_user_task_limit",
  "site.max_pending_tasks",
  "site.workspace_release_version",
  "site.workspace_release_title",
  "site.workspace_release_notes",
  "site.workspace_release_published_at",
  "email.verification_enabled",
  "email.verification_ttl_seconds",
  "email.verification_resend_seconds",
];

const channelTemplate: ImageChannelPayload = {
  name: "",
  channel_code: "",
  channel_type: "image",
  provider_name: "",
  base_url: "",
  api_path: "/images/generations",
  edit_api_path: "",
  model_name: "",
  timeout_seconds: 60,
  max_retries: 3,
  is_active: true,
  priority: 1,
  api_key: "",
};

function cx(...parts: Array<string | false | undefined>) {
  return parts.filter(Boolean).join(" ");
}

function fmtDate(value: string | null | undefined) {
  if (!value) return "--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")} ${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
}

function formatJson(value: Record<string, any> | null | undefined) {
  if (!value || !Object.keys(value).length) return "";
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return "";
  }
}

function statusClass(status: string) {
  const map: Record<string, string> = {
    queued: "bg-slate-100 text-slate-700",
    running: "bg-blue-50 text-blue-700",
    succeeded: "bg-emerald-50 text-emerald-700",
    failed: "bg-rose-50 text-rose-700",
    canceled: "bg-amber-50 text-amber-700",
    active: "bg-emerald-50 text-emerald-700",
    disabled: "bg-slate-100 text-slate-700",
  };
  return map[status] || "bg-slate-100 text-slate-700";
}

export default function App() {
  const root = document.getElementById("console-root");
  const brandName = root?.dataset.brand || "绘镜";
  const currentUser = root?.dataset.user || "admin";

  const [activeTab, setActiveTab] = useState<TabId>("dashboard");
  const [loading, setLoading] = useState(false);
  const [booting, setBooting] = useState(true);
  const [dashboard, setDashboard] = useState<DashboardData | null>(null);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [usersPagination, setUsersPagination] = useState<Pagination | null>(null);
  const [tasks, setTasks] = useState<AdminTask[]>([]);
  const [tasksPagination, setTasksPagination] = useState<Pagination | null>(null);
  const [channels, setChannels] = useState<ImageChannel[]>([]);
  const [configData, setConfigData] = useState<ConfigListResponse | null>(null);
  const [keyword, setKeyword] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [userQuotaModal, setUserQuotaModal] = useState<UserQuotaModalState>({ open: false, user: null });
  const [channelModal, setChannelModal] = useState<ChannelModalState>({ open: false, item: null });
  const [toasts, setToasts] = useState<Array<{ id: string; message: string; type: ToastType }>>([]);

  const addToast = (message: string, type: ToastType = "success") => {
    setToasts((prev) => [...prev, { id: `${Date.now()}-${Math.random()}`, message, type }]);
  };

  const removeToast = (id: string) => {
    setToasts((prev) => prev.filter((item) => item.id !== id));
  };

  const settingsItems = useMemo(() => {
    const items = configData?.items || [];
    return items.filter((item) => trackedSettingKeys.includes(item.key));
  }, [configData]);

  async function loadDashboard() {
    setDashboard(await fetchDashboard());
  }

  async function loadUsers(page = 1) {
    const data = await fetchUsers({ page, per_page: 12, keyword, status: statusFilter });
    setUsers(data.items);
    setUsersPagination(data.pagination);
  }

  async function loadTasks(page = 1) {
    const data = await fetchTasks({ page, per_page: 12, keyword, status: statusFilter });
    setTasks(data.items);
    setTasksPagination(data.pagination);
  }

  async function loadChannels() {
    setChannels(await fetchImageChannels());
  }

  async function loadConfig() {
    setConfigData(await fetchConfig());
  }

  async function refreshCurrent(silent = false) {
    if (!silent) setLoading(true);
    try {
      if (activeTab === "dashboard") await loadDashboard();
      if (activeTab === "users") await loadUsers();
      if (activeTab === "tasks") await loadTasks();
      if (activeTab === "channels") await loadChannels();
      if (activeTab === "settings") await loadConfig();
    } catch (error: any) {
      addToast(error.message || "加载失败", "error");
    } finally {
      if (!silent) setLoading(false);
      setBooting(false);
    }
  }

  useEffect(() => {
    loadDashboard()
      .then(() => setBooting(false))
      .catch((error: any) => {
        addToast(error.message || "初始化失败", "error");
        setBooting(false);
      });
  }, []);

  useEffect(() => {
    refreshCurrent();
  }, [activeTab]);

  useEffect(() => {
    if (activeTab !== "users" && activeTab !== "tasks") return;
    const timer = window.setTimeout(() => refreshCurrent(true), 250);
    return () => window.clearTimeout(timer);
  }, [keyword, statusFilter]);

  const activeTabMeta = tabs.find((tab) => tab.id === activeTab) || tabs[0];

  return (
    <div className="flex h-screen overflow-hidden bg-[#f5f4ee] text-slate-900">
      <aside className="hidden w-72 shrink-0 border-r border-black/5 bg-white/90 p-5 lg:flex lg:flex-col">
        <div className="flex items-center gap-3 rounded-[2rem] bg-black px-4 py-4 text-white shadow-xl">
          <div className="flex h-11 w-11 items-center justify-center rounded-2xl bg-white/10">
            <Sparkles className="h-5 w-5" />
          </div>
          <div>
            <div className="text-lg font-black tracking-tight">{brandName}</div>
            <div className="text-xs uppercase tracking-[0.24em] text-white/50">Admin Console</div>
          </div>
        </div>

        <nav className="mt-8 space-y-2">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              type="button"
              onClick={() => {
                setKeyword("");
                setStatusFilter("");
                setActiveTab(tab.id);
              }}
              className={cx(
                "w-full rounded-[1.4rem] px-4 py-3 text-left transition-all",
                activeTab === tab.id ? "bg-black text-white shadow-lg shadow-black/10" : "bg-white text-slate-500 hover:bg-slate-50 hover:text-black",
              )}
            >
              <div className="flex items-center gap-3">
                <tab.icon className="h-5 w-5 shrink-0" />
                <div>
                  <div className="text-sm font-bold">{tab.label}</div>
                  <div className={cx("text-xs", activeTab === tab.id ? "text-white/60" : "text-slate-400")}>{tab.desc}</div>
                </div>
              </div>
            </button>
          ))}
        </nav>

        <div className="mt-auto rounded-[1.6rem] border border-black/5 bg-white p-4 shadow-sm">
          <div className="flex items-center gap-3">
            <div className="flex h-11 w-11 items-center justify-center rounded-2xl bg-slate-100 text-sm font-black">
              {currentUser.slice(0, 1).toUpperCase()}
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-black text-slate-950">{currentUser}</div>
              <div className="text-xs text-slate-400">管理员</div>
            </div>
            <button
              type="button"
              onClick={() => logout()}
              className="rounded-full border border-slate-200 p-2 text-slate-500 transition-colors hover:border-black hover:text-black"
            >
              <LogOut className="h-4 w-4" />
            </button>
          </div>
        </div>
      </aside>

      <main className="min-w-0 flex-1">
        <header className="sticky top-0 z-20 flex items-center justify-between border-b border-black/5 bg-white/80 px-5 py-4 backdrop-blur md:px-8">
          <div>
            <div className="flex items-center gap-3">
              <activeTabMeta.icon className="h-5 w-5 text-slate-400" />
              <h1 className="text-xl font-black tracking-tight">{activeTabMeta.label}</h1>
              <span className="rounded-full bg-emerald-50 px-2 py-1 text-[10px] font-black uppercase tracking-[0.18em] text-emerald-700">
                Live
              </span>
            </div>
            <p className="mt-1 text-sm text-slate-500">{activeTabMeta.desc}，与创作台保持统一视觉风格</p>
          </div>
          <button
            type="button"
            onClick={() => refreshCurrent()}
            className="rounded-full border border-slate-200 bg-white p-2.5 text-slate-500 transition-colors hover:border-black hover:text-black"
          >
            <RefreshCw className={cx("h-4 w-4", loading && "animate-spin")} />
          </button>
        </header>

        <div className="h-[calc(100vh-73px)] overflow-y-auto px-5 py-6 md:px-8">
          {booting ? (
            <div className="flex h-[60vh] items-center justify-center">
              <Loader2 className="h-8 w-8 animate-spin text-slate-300" />
            </div>
          ) : (
            <>
              {activeTab === "dashboard" && dashboard && (
                <DashboardSection dashboard={dashboard} onJump={setActiveTab} />
              )}
              {activeTab === "users" && (
                <UsersSection
                  users={users}
                  pagination={usersPagination}
                  keyword={keyword}
                  setKeyword={setKeyword}
                  statusFilter={statusFilter}
                  setStatusFilter={setStatusFilter}
                  onPage={loadUsers}
                  onEdit={async (user) => {
                    try {
                      setLoading(true);
                      const detail = await fetchUserDetail(user.id);
                      setUserQuotaModal({ open: true, user: detail });
                    } catch (error: any) {
                      addToast(error.message || "用户详情加载失败", "error");
                    } finally {
                      setLoading(false);
                    }
                  }}
                />
              )}
              {activeTab === "tasks" && (
                <TasksSection
                  tasks={tasks}
                  pagination={tasksPagination}
                  keyword={keyword}
                  setKeyword={setKeyword}
                  statusFilter={statusFilter}
                  setStatusFilter={setStatusFilter}
                  onPage={loadTasks}
                />
              )}
              {activeTab === "channels" && (
                <ChannelsSection
                  channels={channels}
                  onCreate={() => setChannelModal({ open: true, item: null })}
                  onEdit={(item) => setChannelModal({ open: true, item })}
                  onDelete={async (item) => {
                    try {
                      await deleteImageChannel(item.id);
                      await loadChannels();
                      addToast("渠道已删除");
                    } catch (error: any) {
                      addToast(error.message || "删除失败", "error");
                    }
                  }}
                />
              )}
              {activeTab === "settings" && (
                <SettingsSection
                  items={settingsItems}
                  allItems={configData?.items || []}
                  onSave={async (item, value) => {
                    try {
                      await updateConfig(item.key, {
                        value,
                        is_secret: item.is_secret,
                        description: item.description,
                        category: item.category,
                      });
                      await loadConfig();
                      addToast("配置已保存");
                    } catch (error: any) {
                      addToast(error.message || "保存失败", "error");
                    }
                  }}
                  onApplyDefault={async () => {
                    try {
                      await batchUpdateConfig([
                        { key: "site.default_daily_free_quota", value: "3" },
                        { key: "site.register_enabled", value: "1" },
                        { key: "email.verification_enabled", value: "1" },
                      ]);
                      await loadConfig();
                      addToast("默认免费配置已恢复");
                    } catch (error: any) {
                      addToast(error.message || "默认配置应用失败", "error");
                    }
                  }}
                />
              )}
            </>
          )}
        </div>
      </main>

      {userQuotaModal.open && userQuotaModal.user && (
        <UserQuotaModal
          user={userQuotaModal.user}
          onClose={() => setUserQuotaModal({ open: false, user: null })}
          onSaved={async (payload) => {
            try {
              await updateUserQuota(userQuotaModal.user!.id, payload);
              await loadUsers(usersPagination?.page || 1);
              const detail = await fetchUserDetail(userQuotaModal.user!.id);
              setUserQuotaModal({ open: true, user: detail });
              addToast("用户免费次数已更新");
            } catch (error: any) {
              addToast(error.message || "更新失败", "error");
            }
          }}
        />
      )}

      {channelModal.open && (
        <ChannelModal
          item={channelModal.item}
          onClose={() => setChannelModal({ open: false, item: null })}
          onSaved={async (payload) => {
            try {
              if (channelModal.item) {
                await updateImageChannel(channelModal.item.id, payload);
              } else {
                await createImageChannel(payload);
              }
              await loadChannels();
              setChannelModal({ open: false, item: null });
              addToast(channelModal.item ? "渠道已更新" : "渠道已创建");
            } catch (error: any) {
              addToast(error.message || "渠道保存失败", "error");
            }
          }}
        />
      )}

      <ToastContainer toasts={toasts} onClose={removeToast} />
    </div>
  );
}

function DashboardSection({ dashboard, onJump }: { dashboard: DashboardData; onJump: (tab: TabId) => void }) {
  const successRate = dashboard.today.tasks ? Math.round((dashboard.today.succeeded_tasks / dashboard.today.tasks) * 100) : 0;
  const cards = [
    { label: "总账号数", value: dashboard.totals.users, icon: Users, hint: `今日新增 ${dashboard.today.new_users}` },
    { label: "总任务数", value: dashboard.totals.tasks, icon: Wand2, hint: `今日 ${dashboard.today.tasks}` },
    { label: "成功率", value: `${successRate}%`, icon: BadgeCheck, hint: `${dashboard.today.failed_tasks} 个失败` },
    { label: "图片总数", value: dashboard.totals.images, icon: ImageIcon, hint: `${dashboard.totals.active_channels} 个启用渠道` },
  ];

  return (
    <div className="space-y-6">
      <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {cards.map((card) => (
          <div key={card.label} className="rounded-[1.8rem] border border-black/5 bg-white p-5 shadow-sm">
            <div className="flex items-center justify-between">
              <div className="text-sm font-bold text-slate-500">{card.label}</div>
              <div className="rounded-2xl bg-slate-100 p-2 text-slate-600">
                <card.icon className="h-4 w-4" />
              </div>
            </div>
            <div className="mt-4 text-3xl font-black tracking-tight">{card.value}</div>
            <div className="mt-2 text-xs text-slate-400">{card.hint}</div>
          </div>
        ))}
      </section>

      <section className="grid gap-6 xl:grid-cols-[1.1fr_0.9fr]">
        <div className="rounded-[2rem] border border-black/5 bg-white p-5 shadow-sm">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h2 className="text-lg font-black tracking-tight">最新任务</h2>
              <p className="text-sm text-slate-500">快速检查失败任务与排队情况</p>
            </div>
            <button type="button" onClick={() => onJump("tasks")} className="text-xs font-bold text-slate-500 hover:text-black">
              查看全部
            </button>
          </div>
          <div className="space-y-3">
            {dashboard.latest_tasks.map((task) => (
              <div key={task.task_no} className="rounded-[1.4rem] border border-black/5 bg-slate-50/70 p-4">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="max-h-24 overflow-y-auto whitespace-pre-wrap break-words pr-1 text-sm font-bold leading-6">
                      {task.prompt}
                    </div>
                    <div className="mt-1 text-xs text-slate-500">
                      {task.username} · {task.channel_name || "未分配渠道"} · {fmtDate(task.created_at)}
                    </div>
                  </div>
                  <span className={cx("rounded-full px-2 py-1 text-[11px] font-bold", statusClass(task.status))}>{task.status}</span>
                </div>
                {task.error_message && <div className="mt-3 text-xs text-rose-600">{task.error_message}</div>}
              </div>
            ))}
          </div>
        </div>

        <div className="space-y-6">
          <div className="rounded-[2rem] border border-black/5 bg-white p-5 shadow-sm">
            <div className="mb-4 flex items-center justify-between">
              <div>
                <h2 className="text-lg font-black tracking-tight">最新失败日志</h2>
                <p className="text-sm text-slate-500">保留上游错误原因，方便快速溯源。</p>
              </div>
              <button type="button" onClick={() => onJump("tasks")} className="text-xs font-bold text-slate-500 hover:text-black">
                查看任务
              </button>
            </div>
            <div className="space-y-3">
              {dashboard.recent_failures.length ? (
                dashboard.recent_failures.map((log) => (
                  <div key={log.id} className="rounded-[1.4rem] border border-rose-100 bg-rose-50/70 p-4">
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <div className="text-sm font-bold text-rose-900">{log.task_no || "未绑定任务号"}</div>
                        <div className="mt-1 text-xs text-rose-700">{fmtDate(log.created_at)} · {log.source}</div>
                      </div>
                      <span className="rounded-full bg-white/80 px-2 py-1 text-[11px] font-bold text-rose-700">
                        {log.error_code || "generation_failed"}
                      </span>
                    </div>
                    <div className="mt-3 max-h-28 overflow-y-auto rounded-2xl bg-white/80 px-3 py-2 text-xs leading-6 text-rose-800">
                      {log.error_message || log.remark || "未记录错误详情"}
                    </div>
                    {(log.provider_request_id || Object.keys(log.payload_json || {}).length > 0) && (
                      <div className="mt-3 space-y-2 rounded-2xl bg-white/80 px-3 py-3 text-xs text-slate-600">
                        {log.provider_request_id && <div>上游请求 ID：{log.provider_request_id}</div>}
                        {Object.keys(log.payload_json || {}).length > 0 && (
                          <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded-xl bg-slate-950 px-3 py-2 text-[11px] leading-5 text-slate-100">
                            {formatJson(log.payload_json)}
                          </pre>
                        )}
                      </div>
                    )}
                  </div>
                ))
              ) : (
                <div className="rounded-[1.4rem] border border-black/5 bg-slate-50/70 p-4 text-sm text-slate-500">
                  暂无失败日志。
                </div>
              )}
            </div>
          </div>

          <div className="rounded-[2rem] border border-black/5 bg-white p-5 shadow-sm">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <h2 className="text-lg font-black tracking-tight">最新注册</h2>
              <p className="text-sm text-slate-500">查看新账号的默认免费次数是否正确</p>
            </div>
            <button type="button" onClick={() => onJump("users")} className="text-xs font-bold text-slate-500 hover:text-black">
              查看全部
            </button>
          </div>
          <div className="space-y-3">
            {dashboard.latest_users.map((user) => (
              <div key={user.id} className="flex items-center justify-between rounded-[1.4rem] border border-black/5 bg-slate-50/70 p-4">
                <div>
                  <div className="text-sm font-bold">{user.username}</div>
                  <div className="mt-1 text-xs text-slate-500">{user.email || "未绑定邮箱"}</div>
                </div>
                <div className="text-right">
                  <div className="text-sm font-black">{user.quota?.available_quota ?? 0}</div>
                  <div className="text-[11px] text-slate-400">剩余次数</div>
                </div>
              </div>
            ))}
          </div>
        </div>
        </div>
      </section>
    </div>
  );
}

function UsersSection({
  users,
  pagination,
  keyword,
  setKeyword,
  statusFilter,
  setStatusFilter,
  onPage,
  onEdit,
}: {
  users: AdminUser[];
  pagination: Pagination | null;
  keyword: string;
  setKeyword: (value: string) => void;
  statusFilter: string;
  setStatusFilter: (value: string) => void;
  onPage: (page: number) => void;
  onEdit: (user: AdminUser) => void;
}) {
  return (
    <section className="rounded-[2rem] border border-black/5 bg-white p-5 shadow-sm">
      <ListToolbar
        keyword={keyword}
        setKeyword={setKeyword}
        statusFilter={statusFilter}
        setStatusFilter={setStatusFilter}
        placeholder="搜索用户名或邮箱"
        statuses={[
          { value: "", label: "全部状态" },
          { value: "active", label: "正常" },
          { value: "disabled", label: "禁用" },
          { value: "locked", label: "锁定" },
        ]}
      />
      <div className="mt-5 overflow-hidden rounded-[1.4rem] border border-black/5">
        <table className="min-w-full divide-y divide-black/5 text-sm">
          <thead className="bg-slate-50 text-left text-xs uppercase tracking-[0.16em] text-slate-400">
            <tr>
              <th className="px-4 py-3">用户</th>
              <th className="px-4 py-3">每日次数</th>
              <th className="px-4 py-3">当前剩余</th>
              <th className="px-4 py-3">冻结</th>
              <th className="px-4 py-3">注册时间</th>
              <th className="px-4 py-3"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-black/5">
            {users.map((user) => (
              <tr key={user.id} className="bg-white">
                <td className="px-4 py-4">
                  <div className="font-bold">{user.username}</div>
                  <div className="mt-1 text-xs text-slate-500">{user.email || "未绑定邮箱"}</div>
                </td>
                <td className="px-4 py-4">{user.quota?.daily_quota ?? "--"}</td>
                <td className="px-4 py-4 font-bold">{user.quota?.available_quota ?? "--"}</td>
                <td className="px-4 py-4">{user.quota?.frozen_quota ?? "--"}</td>
                <td className="px-4 py-4 text-slate-500">{fmtDate(user.date_joined)}</td>
                <td className="px-4 py-4 text-right">
                  <button type="button" onClick={() => onEdit(user)} className="rounded-full border border-slate-200 px-3 py-1.5 text-xs font-bold text-slate-600 hover:border-black hover:text-black">
                    修改次数
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <PaginationBar pagination={pagination} onPage={onPage} />
    </section>
  );
}

function TasksSection({
  tasks,
  pagination,
  keyword,
  setKeyword,
  statusFilter,
  setStatusFilter,
  onPage,
}: {
  tasks: AdminTask[];
  pagination: Pagination | null;
  keyword: string;
  setKeyword: (value: string) => void;
  statusFilter: string;
  setStatusFilter: (value: string) => void;
  onPage: (page: number) => void;
}) {
  return (
    <section className="rounded-[2rem] border border-black/5 bg-white p-5 shadow-sm">
      <ListToolbar
        keyword={keyword}
        setKeyword={setKeyword}
        statusFilter={statusFilter}
        setStatusFilter={setStatusFilter}
        placeholder="搜索任务号、用户或提示词"
        statuses={[
          { value: "", label: "全部状态" },
          { value: "queued", label: "排队中" },
          { value: "running", label: "执行中" },
          { value: "succeeded", label: "成功" },
          { value: "failed", label: "失败" },
          { value: "canceled", label: "已取消" },
        ]}
      />
      <div className="mt-5 space-y-3">
        {tasks.map((task) => (
          <article key={task.task_no} className="rounded-[1.5rem] border border-black/5 bg-slate-50/70 p-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="min-w-0 flex-1">
                <div className="max-h-28 overflow-y-auto whitespace-pre-wrap break-words pr-1 text-sm font-bold leading-6">
                  {task.prompt}
                </div>
                <div className="mt-1 text-xs text-slate-500">
                  {task.task_no} · {task.username} · {task.channel_name || "未分配渠道"}
                </div>
              </div>
              <span className={cx("rounded-full px-2 py-1 text-[11px] font-bold", statusClass(task.status))}>{task.status}</span>
            </div>
            <div className="mt-3 grid gap-3 text-xs text-slate-500 md:grid-cols-4">
              <div>尺寸：{task.size_code}</div>
              <div>风格：{task.style_code}</div>
              <div>创建：{fmtDate(task.created_at)}</div>
              <div>结束：{fmtDate(task.finished_at)}</div>
            </div>
            {task.error_message && <div className="mt-3 rounded-2xl bg-rose-50 px-3 py-2 text-xs text-rose-700">{task.error_message}</div>}
            {(task.provider_request_id || Object.keys(task.provider_response_json || {}).length > 0) && (
              <div className="mt-3 space-y-2 rounded-2xl border border-black/5 bg-white px-3 py-3 text-xs text-slate-600">
                {task.provider_request_id && <div>上游请求 ID：{task.provider_request_id}</div>}
                {Object.keys(task.provider_response_json || {}).length > 0 && (
                  <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-xl bg-slate-950 px-3 py-2 text-[11px] leading-5 text-slate-100">
                    {formatJson(task.provider_response_json)}
                  </pre>
                )}
              </div>
            )}
          </article>
        ))}
      </div>
      <PaginationBar pagination={pagination} onPage={onPage} />
    </section>
  );
}

function ChannelsSection({
  channels,
  onCreate,
  onEdit,
  onDelete,
}: {
  channels: ImageChannel[];
  onCreate: () => void;
  onEdit: (item: ImageChannel) => void;
  onDelete: (item: ImageChannel) => void;
}) {
  return (
    <section className="rounded-[2rem] border border-black/5 bg-white p-5 shadow-sm">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-lg font-black tracking-tight">渠道管理</h2>
          <p className="text-sm text-slate-500">保留参考项目的渠道模型，继续支持 image / llm 两类配置。</p>
        </div>
        <button type="button" onClick={onCreate} className="rounded-full bg-black px-4 py-2 text-sm font-bold text-white shadow-lg shadow-black/10">
          新建渠道
        </button>
      </div>
      <div className="mt-5 grid gap-4 xl:grid-cols-2">
        {channels.map((channel) => (
          <article key={channel.id} className="rounded-[1.6rem] border border-black/5 bg-slate-50/70 p-5">
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="text-lg font-black tracking-tight">{channel.name}</div>
                <div className="mt-1 text-xs text-slate-500">
                  {channel.channel_code} · {channel.provider_name || "未填写服务方"} · {channel.channel_type}
                </div>
              </div>
              <span className={cx("rounded-full px-2 py-1 text-[11px] font-bold", channel.is_active ? "bg-emerald-50 text-emerald-700" : "bg-slate-100 text-slate-600")}>
                {channel.is_active ? "启用中" : "已停用"}
              </span>
            </div>
            <div className="mt-4 text-xs leading-6 text-slate-500">
              <div>Base URL：{channel.base_url}</div>
              <div>API Path：{channel.api_path}</div>
              <div>图生图 Path：{channel.edit_api_path || "自动推导"}</div>
              <div>Model：{channel.model_name}</div>
            </div>
            <div className="mt-4 flex gap-2">
              <button type="button" onClick={() => onEdit(channel)} className="rounded-full border border-slate-200 px-3 py-1.5 text-xs font-bold text-slate-600 hover:border-black hover:text-black">
                编辑
              </button>
              <button type="button" onClick={() => onDelete(channel)} className="rounded-full border border-rose-200 px-3 py-1.5 text-xs font-bold text-rose-600 hover:bg-rose-50">
                删除
              </button>
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}

function SettingsSection({
  items,
  allItems,
  onSave,
  onApplyDefault,
}: {
  items: SiteConfigItem[];
  allItems: SiteConfigItem[];
  onSave: (item: SiteConfigItem, value: string) => Promise<void>;
  onApplyDefault: () => Promise<void>;
}) {
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const releaseItems = items.filter((item) => item.key.startsWith("site.workspace_release_"));
  const basicItems = items.filter((item) => !item.key.startsWith("site.workspace_release_"));

  useEffect(() => {
    const next: Record<string, string> = {};
    items.forEach((item) => {
      next[item.key] = item.value;
    });
    setDrafts(next);
  }, [items]);

  return (
    <section className="space-y-6">
      <div className="rounded-[2rem] border border-black/5 bg-white p-5 shadow-sm">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-lg font-black tracking-tight">基础设置</h2>
            <p className="text-sm text-slate-500">重点保留免费版部署必须的配置项。</p>
          </div>
          <button type="button" onClick={onApplyDefault} className="rounded-full border border-slate-200 px-4 py-2 text-sm font-bold text-slate-600 hover:border-black hover:text-black">
            恢复推荐默认值
          </button>
        </div>
        <div className="mt-5 grid gap-4 md:grid-cols-2">
          {basicItems.map((item) => (
            <div key={item.key} className="rounded-[1.5rem] border border-black/5 bg-slate-50/70 p-4">
              <div className="text-sm font-bold">{item.description || item.key}</div>
              <div className="mt-1 text-xs text-slate-400">{item.key}</div>
              <input
                value={drafts[item.key] ?? ""}
                onChange={(event) => setDrafts((prev) => ({ ...prev, [item.key]: event.target.value }))}
                className="mt-4 w-full rounded-2xl border border-slate-200 bg-white px-4 py-3 text-sm outline-none transition-colors focus:border-black"
              />
              <button
                type="button"
                onClick={() => onSave(item, drafts[item.key] ?? "")}
                className="mt-3 rounded-full bg-black px-4 py-2 text-xs font-bold text-white shadow-lg shadow-black/10"
              >
                保存
              </button>
            </div>
          ))}
        </div>
      </div>

      <div className="rounded-[2rem] border border-black/5 bg-white p-5 shadow-sm">
        <div>
          <h2 className="text-lg font-black tracking-tight">版本与更新日志</h2>
          <p className="text-sm text-slate-500">这里修改后，工作台版本号和更新弹窗会同步生效。</p>
        </div>
        <div className="mt-5 grid gap-4">
          {releaseItems.map((item) => {
            const isLongText = item.key === "site.workspace_release_notes";
            return (
              <div key={item.key} className="rounded-[1.5rem] border border-black/5 bg-slate-50/70 p-4">
                <div className="text-sm font-bold">{item.description || item.key}</div>
                <div className="mt-1 text-xs text-slate-400">{item.key}</div>
                {isLongText ? (
                  <textarea
                    value={drafts[item.key] ?? ""}
                    onChange={(event) => setDrafts((prev) => ({ ...prev, [item.key]: event.target.value }))}
                    rows={8}
                    className="mt-4 w-full rounded-2xl border border-slate-200 bg-white px-4 py-3 text-sm outline-none transition-colors focus:border-black"
                    placeholder="每行一条更新说明"
                  />
                ) : (
                  <input
                    value={drafts[item.key] ?? ""}
                    onChange={(event) => setDrafts((prev) => ({ ...prev, [item.key]: event.target.value }))}
                    className="mt-4 w-full rounded-2xl border border-slate-200 bg-white px-4 py-3 text-sm outline-none transition-colors focus:border-black"
                  />
                )}
                <button
                  type="button"
                  onClick={() => onSave(item, drafts[item.key] ?? "")}
                  className="mt-3 rounded-full bg-black px-4 py-2 text-xs font-bold text-white shadow-lg shadow-black/10"
                >
                  保存
                </button>
              </div>
            );
          })}
        </div>
      </div>

      <div className="rounded-[2rem] border border-black/5 bg-white p-5 shadow-sm">
        <h2 className="text-lg font-black tracking-tight">完整配置预览</h2>
        <p className="mt-1 text-sm text-slate-500">后台只内置核心配置项，其余值仍可在这里查看。</p>
        <div className="mt-5 overflow-hidden rounded-[1.4rem] border border-black/5">
          <table className="min-w-full divide-y divide-black/5 text-sm">
            <thead className="bg-slate-50 text-left text-xs uppercase tracking-[0.16em] text-slate-400">
              <tr>
                <th className="px-4 py-3">Key</th>
                <th className="px-4 py-3">Value</th>
                <th className="px-4 py-3">分类</th>
                <th className="px-4 py-3">更新时间</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-black/5">
              {allItems.map((item) => (
                <tr key={item.key}>
                  <td className="px-4 py-3 text-xs font-bold text-slate-600">{item.key}</td>
                  <td className="px-4 py-3 text-xs text-slate-500">{item.value || "--"}</td>
                  <td className="px-4 py-3 text-xs text-slate-500">{item.category}</td>
                  <td className="px-4 py-3 text-xs text-slate-500">{fmtDate(item.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}

function ListToolbar({
  keyword,
  setKeyword,
  statusFilter,
  setStatusFilter,
  placeholder,
  statuses,
}: {
  keyword: string;
  setKeyword: (value: string) => void;
  statusFilter: string;
  setStatusFilter: (value: string) => void;
  placeholder: string;
  statuses: Array<{ value: string; label: string }>;
}) {
  return (
    <div className="flex flex-wrap gap-3">
      <input
        value={keyword}
        onChange={(event) => setKeyword(event.target.value)}
        placeholder={placeholder}
        className="min-w-[280px] flex-1 rounded-full border border-slate-200 bg-slate-50 px-4 py-3 text-sm outline-none transition-colors focus:border-black"
      />
      <select
        value={statusFilter}
        onChange={(event) => setStatusFilter(event.target.value)}
        className="rounded-full border border-slate-200 bg-slate-50 px-4 py-3 text-sm outline-none transition-colors focus:border-black"
      >
        {statuses.map((status) => (
          <option key={status.value} value={status.value}>
            {status.label}
          </option>
        ))}
      </select>
    </div>
  );
}

function PaginationBar({ pagination, onPage }: { pagination: Pagination | null; onPage: (page: number) => void }) {
  if (!pagination || pagination.total_pages <= 1) return null;
  return (
    <div className="mt-5 flex items-center justify-between text-sm text-slate-500">
      <div>
        第 {pagination.page} / {pagination.total_pages} 页，共 {pagination.total} 条
      </div>
      <div className="flex gap-2">
        <button
          type="button"
          disabled={!pagination.has_previous}
          onClick={() => onPage(pagination.page - 1)}
          className="rounded-full border border-slate-200 px-3 py-1.5 disabled:cursor-not-allowed disabled:opacity-40"
        >
          上一页
        </button>
        <button
          type="button"
          disabled={!pagination.has_next}
          onClick={() => onPage(pagination.page + 1)}
          className="rounded-full border border-slate-200 px-3 py-1.5 disabled:cursor-not-allowed disabled:opacity-40"
        >
          下一页
        </button>
      </div>
    </div>
  );
}

function UserQuotaModal({
  user,
  onClose,
  onSaved,
}: {
  user: AdminUserDetail;
  onClose: () => void;
  onSaved: (payload: { daily_quota?: number; available_quota?: number; remark?: string }) => Promise<void>;
}) {
  const [dailyQuota, setDailyQuota] = useState(String(user.quota?.daily_quota ?? 3));
  const [availableQuota, setAvailableQuota] = useState(String(user.quota?.available_quota ?? 0));
  const [remark, setRemark] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setDailyQuota(String(user.quota?.daily_quota ?? 3));
    setAvailableQuota(String(user.quota?.available_quota ?? 0));
    setRemark("");
  }, [user]);

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    try {
      await onSaved({
        daily_quota: Number(dailyQuota),
        available_quota: Number(availableQuota),
        remark,
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <ModalShell title={`修改用户次数 · ${user.username}`} onClose={onClose}>
      <form onSubmit={handleSubmit} className="space-y-4">
        <div className="grid gap-4 md:grid-cols-2">
          <Field label="每日免费次数">
            <input value={dailyQuota} onChange={(e) => setDailyQuota(e.target.value)} className={fieldClass} />
          </Field>
          <Field label="当前剩余次数">
            <input value={availableQuota} onChange={(e) => setAvailableQuota(e.target.value)} className={fieldClass} />
          </Field>
        </div>
        <Field label="备注">
          <input value={remark} onChange={(e) => setRemark(e.target.value)} className={fieldClass} placeholder="例如：手动补偿 2 次" />
        </Field>
        <div className="rounded-[1.4rem] bg-slate-50 p-4 text-sm text-slate-500">
          <div>邮箱：{user.email || "--"}</div>
          <div>冻结次数：{user.quota?.frozen_quota ?? 0}</div>
          <div>累计成功生成：{user.quota?.total_generated_count ?? 0}</div>
          <div>最近刷新日期：{user.quota?.last_reset_date || "--"}</div>
        </div>
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded-full border border-slate-200 px-4 py-2 text-sm font-bold text-slate-600">
            取消
          </button>
          <button type="submit" disabled={saving} className="rounded-full bg-black px-4 py-2 text-sm font-bold text-white">
            {saving ? "保存中..." : "保存"}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function ChannelModal({
  item,
  onClose,
  onSaved,
}: {
  item: ImageChannel | null;
  onClose: () => void;
  onSaved: (payload: ImageChannelPayload) => Promise<void>;
}) {
  const [form, setForm] = useState<ImageChannelPayload>(() =>
    item
      ? {
          name: item.name,
          channel_code: item.channel_code,
          channel_type: item.channel_type,
          provider_name: item.provider_name,
          base_url: item.base_url,
          api_path: item.api_path,
          edit_api_path: item.edit_api_path,
          model_name: item.model_name,
          timeout_seconds: item.timeout_seconds,
          max_retries: item.max_retries,
          is_active: item.is_active,
          priority: item.priority,
          api_key: "",
        }
      : channelTemplate,
  );
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setForm(
      item
        ? {
            name: item.name,
            channel_code: item.channel_code,
            channel_type: item.channel_type,
            provider_name: item.provider_name,
            base_url: item.base_url,
            api_path: item.api_path,
            edit_api_path: item.edit_api_path,
            model_name: item.model_name,
            timeout_seconds: item.timeout_seconds,
            max_retries: item.max_retries,
            is_active: item.is_active,
            priority: item.priority,
            api_key: "",
          }
        : channelTemplate,
    );
  }, [item]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    try {
      await onSaved(form);
    } finally {
      setSaving(false);
    }
  };

  return (
    <ModalShell title={item ? `编辑渠道 · ${item.name}` : "新建渠道"} onClose={onClose}>
      <form onSubmit={submit} className="space-y-4">
        <div className="grid gap-4 md:grid-cols-2">
          <Field label="名称">
            <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} className={fieldClass} />
          </Field>
          <Field label="编码">
            <input value={form.channel_code} onChange={(e) => setForm({ ...form, channel_code: e.target.value })} className={fieldClass} disabled={!!item} />
          </Field>
          <Field label="类型">
            <select value={form.channel_type} onChange={(e) => setForm({ ...form, channel_type: e.target.value as "image" | "llm" })} className={fieldClass}>
              <option value="image">image</option>
              <option value="llm">llm</option>
            </select>
          </Field>
          <Field label="服务方">
            <input value={form.provider_name} onChange={(e) => setForm({ ...form, provider_name: e.target.value })} className={fieldClass} />
          </Field>
          <Field label="Base URL">
            <input value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} className={fieldClass} />
          </Field>
          <Field label="API Path">
            <input value={form.api_path} onChange={(e) => setForm({ ...form, api_path: e.target.value })} className={fieldClass} />
          </Field>
          <Field label="图生图 API Path">
            <input
              value={form.edit_api_path}
              onChange={(e) => setForm({ ...form, edit_api_path: e.target.value })}
              className={fieldClass}
              placeholder="留空则自动从 API Path 推导"
            />
          </Field>
          <Field label="Model Name">
            <input value={form.model_name} onChange={(e) => setForm({ ...form, model_name: e.target.value })} className={fieldClass} />
          </Field>
          <Field label="API Key">
            <input value={form.api_key || ""} onChange={(e) => setForm({ ...form, api_key: e.target.value })} className={fieldClass} placeholder={item ? "留空表示不修改" : ""} />
          </Field>
          <Field label="超时秒数">
            <input value={form.timeout_seconds} onChange={(e) => setForm({ ...form, timeout_seconds: Number(e.target.value) })} className={fieldClass} />
          </Field>
          <Field label="最大重试">
            <input value={form.max_retries} onChange={(e) => setForm({ ...form, max_retries: Number(e.target.value) })} className={fieldClass} />
          </Field>
          <Field label="优先级">
            <input value={form.priority} onChange={(e) => setForm({ ...form, priority: Number(e.target.value) })} className={fieldClass} />
          </Field>
          <Field label="是否启用">
            <select value={form.is_active ? "1" : "0"} onChange={(e) => setForm({ ...form, is_active: e.target.value === "1" })} className={fieldClass}>
              <option value="1">启用</option>
              <option value="0">停用</option>
            </select>
          </Field>
        </div>
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded-full border border-slate-200 px-4 py-2 text-sm font-bold text-slate-600">
            取消
          </button>
          <button type="submit" disabled={saving} className="rounded-full bg-black px-4 py-2 text-sm font-bold text-white">
            {saving ? "保存中..." : "保存"}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function ModalShell({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/35 p-4 backdrop-blur-sm">
      <div className="w-full max-w-3xl rounded-[2rem] bg-white p-6 shadow-2xl">
        <div className="mb-5 flex items-center justify-between">
          <h2 className="text-lg font-black tracking-tight">{title}</h2>
          <button type="button" onClick={onClose} className="rounded-full border border-slate-200 px-3 py-1 text-sm text-slate-500">
            关闭
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block">
      <div className="mb-2 text-sm font-bold text-slate-700">{label}</div>
      {children}
    </label>
  );
}

const fieldClass =
  "w-full rounded-2xl border border-slate-200 bg-slate-50 px-4 py-3 text-sm outline-none transition-colors focus:border-black";
