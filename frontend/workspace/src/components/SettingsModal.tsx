import { useEffect, useMemo, useState, type ReactNode } from "react";
import { AnimatePresence, motion } from "motion/react";
import {
  BadgeCheck,
  Clock3,
  Copy,
  Database,
  Image as ImageIcon,
  KeyRound,
  Loader2,
  Mail,
  Save,
  Shield,
  Sparkles,
  User,
  X,
} from "lucide-react";
import {
  fetchAccountSettings,
  updateAccountPassword,
  updateAccountSettings,
  type AccountSettings,
} from "../services/apiService";

export type SettingsTabId = "profile" | "security" | "quota" | "support";

type NoticeTone = "success" | "error" | "info";

function formatDate(value?: string | null) {
  if (!value) return "--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "--";
  return date.toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function Notice({ tone, message }: { tone: NoticeTone; message: string }) {
  const toneClass =
    tone === "success"
      ? "border-emerald-100 bg-emerald-50 text-emerald-700"
      : tone === "error"
        ? "border-red-100 bg-red-50 text-red-600"
        : "border-slate-200 bg-slate-50 text-slate-600";

  return (
    <div className={`rounded-2xl border px-4 py-3 text-sm font-medium ${toneClass}`}>
      {message}
    </div>
  );
}

function FieldShell({
  label,
  children,
  hint,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
}) {
  return (
    <label className="space-y-2">
      <div className="text-[10px] font-black uppercase tracking-[0.22em] text-gray-400">{label}</div>
      {children}
      {hint ? <p className="text-xs text-gray-400">{hint}</p> : null}
    </label>
  );
}

function ReadonlyField({ value }: { value: string }) {
  return (
    <div className="rounded-2xl border border-gray-100 bg-gray-50 px-4 py-3 text-sm font-medium text-gray-700">
      {value || "--"}
    </div>
  );
}

export default function SettingsModal({
  onClose,
  initialTab = "profile",
  t,
}: {
  onClose: () => void;
  initialTab?: SettingsTabId;
  t: any;
}) {
  const [activeTab, setActiveTab] = useState<SettingsTabId>(initialTab);
  const [settings, setSettings] = useState<AccountSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [notice, setNotice] = useState<{ tone: NoticeTone; message: string } | null>(null);
  const [profileSaving, setProfileSaving] = useState(false);
  const [passwordSaving, setPasswordSaving] = useState(false);
  const [profileForm, setProfileForm] = useState({
    nickname: "",
    email: "",
    avatar: "",
    remark: "",
  });
  const [passwordForm, setPasswordForm] = useState({
    current_password: "",
    new_password: "",
    confirm_password: "",
  });

  const loadSettings = async () => {
    setLoading(true);
    try {
      const data = await fetchAccountSettings();
      setSettings(data);
      setProfileForm({
        nickname: data.user.nickname || data.user.username,
        email: data.user.email || "",
        avatar: data.user.avatar || "",
        remark: data.user.remark || "",
      });
      setNotice(null);
    } catch (error: any) {
      setNotice({ tone: "error", message: error.message || "设置数据加载失败" });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadSettings();
  }, []);

  useEffect(() => {
    setActiveTab(initialTab);
  }, [initialTab]);

  const avatarUrl = useMemo(() => {
    const username = settings?.user.username || "user";
    if (profileForm.avatar.trim()) return profileForm.avatar.trim();
    return `https://api.dicebear.com/7.x/avataaars/svg?seed=${encodeURIComponent(username)}`;
  }, [profileForm.avatar, settings?.user.username]);

  const supportTemplate = useMemo(() => {
    if (!settings) return "";
    return [
      `账号：${settings.user.username}`,
      `联系邮箱：${settings.user.email || "未设置"}`,
      `账户状态：${settings.user.status}`,
      `最近登录：${formatDate(settings.user.last_login_at)}`,
      `可用次数：${settings.quota.available_quota}`,
      `冻结次数：${settings.quota.frozen_quota}`,
      "",
      "问题描述：",
      "复现步骤：",
      "预期结果：",
      "实际结果：",
    ].join("\n");
  }, [settings]);

  const tabs: Array<{ id: SettingsTabId; label: string; tag: string; icon: typeof User }> = [
    { id: "profile", label: "个人资料", tag: "PROFILE", icon: User },
    { id: "security", label: "安全中心", tag: "SECURITY", icon: Shield },
    { id: "quota", label: "配额信息", tag: "QUOTA", icon: Database },
    { id: "support", label: "支持反馈", tag: "SUPPORT", icon: Mail },
  ];

  const copySupportTemplate = async () => {
    if (!supportTemplate) return;
    try {
      await window.navigator.clipboard.writeText(supportTemplate);
      setNotice({ tone: "success", message: "反馈模板已复制，可直接发给管理员排查" });
    } catch {
      setNotice({ tone: "error", message: "复制失败，请手动选中模板内容" });
    }
  };

  const handleSaveProfile = async () => {
    setProfileSaving(true);
    try {
      const data = await updateAccountSettings({
        nickname: profileForm.nickname,
        email: profileForm.email,
        avatar: profileForm.avatar,
        remark: profileForm.remark,
      });
      setSettings(data);
      setProfileForm({
        nickname: data.user.nickname || data.user.username,
        email: data.user.email || "",
        avatar: data.user.avatar || "",
        remark: data.user.remark || "",
      });
      setNotice({ tone: "success", message: "个人资料已保存" });
    } catch (error: any) {
      setNotice({ tone: "error", message: error.message || "个人资料保存失败" });
    } finally {
      setProfileSaving(false);
    }
  };

  const handleChangePassword = async () => {
    if (!passwordForm.current_password || !passwordForm.new_password || !passwordForm.confirm_password) {
      setNotice({ tone: "error", message: "请完整填写密码信息" });
      return;
    }
    setPasswordSaving(true);
    try {
      await updateAccountPassword(passwordForm);
      setPasswordForm({ current_password: "", new_password: "", confirm_password: "" });
      setNotice({ tone: "success", message: "密码已更新" });
    } catch (error: any) {
      setNotice({ tone: "error", message: error.message || "密码更新失败" });
    } finally {
      setPasswordSaving(false);
    }
  };

  const renderProfile = () => {
    if (!settings) return null;
    return (
      <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} className="space-y-8">
        <div className="flex flex-col gap-6 rounded-[2.25rem] border border-gray-100 bg-gray-50/60 p-6 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex items-center gap-5">
            <div className="h-24 w-24 overflow-hidden rounded-[2rem] border-4 border-white bg-white shadow-xl">
              <img src={avatarUrl} alt={`${settings.user.username}头像`} className="h-full w-full object-cover" />
            </div>
            <div className="space-y-2">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="text-2xl font-black tracking-tight text-gray-950">{profileForm.nickname || settings.user.username}</h3>
                <span className="rounded-full bg-black px-3 py-1 text-[10px] font-black uppercase tracking-[0.18em] text-white">
                  {settings.user.role}
                </span>
                <span className="rounded-full bg-white px-3 py-1 text-[10px] font-black uppercase tracking-[0.18em] text-gray-400 shadow-sm">
                  {settings.user.status}
                </span>
              </div>
              <p className="text-sm text-gray-500">账号：{settings.user.username}</p>
              <p className="text-sm text-gray-500">邮箱：{profileForm.email || "未设置"}</p>
            </div>
          </div>
          <div className="rounded-[1.5rem] border border-white bg-white px-4 py-3 shadow-sm">
            <p className="text-[10px] font-black uppercase tracking-[0.2em] text-gray-300">注册来源</p>
            <p className="mt-2 text-sm font-bold text-gray-700">{settings.user.register_source || "website"}</p>
          </div>
        </div>

        <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
          <FieldShell label="昵称">
            <input
              value={profileForm.nickname}
              onChange={(event) => setProfileForm((current) => ({ ...current, nickname: event.target.value }))}
              className="w-full rounded-2xl border border-gray-100 bg-white px-4 py-3 text-sm font-medium text-gray-900 outline-none transition focus:border-black/20 focus:ring-4 focus:ring-black/5"
              placeholder="请输入昵称"
            />
          </FieldShell>

          <FieldShell label="邮箱">
            <div className="relative">
              <Mail className="pointer-events-none absolute left-4 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-300" />
              <input
                value={profileForm.email}
                onChange={(event) => setProfileForm((current) => ({ ...current, email: event.target.value }))}
                className="w-full rounded-2xl border border-gray-100 bg-white py-3 pl-11 pr-4 text-sm font-medium text-gray-900 outline-none transition focus:border-black/20 focus:ring-4 focus:ring-black/5"
                placeholder="name@example.com"
              />
            </div>
          </FieldShell>

          <FieldShell label="头像链接" hint="留空则继续使用默认头像。支持 http / https。">
            <div className="relative">
              <ImageIcon className="pointer-events-none absolute left-4 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-300" />
              <input
                value={profileForm.avatar}
                onChange={(event) => setProfileForm((current) => ({ ...current, avatar: event.target.value }))}
                className="w-full rounded-2xl border border-gray-100 bg-white py-3 pl-11 pr-4 text-sm font-medium text-gray-900 outline-none transition focus:border-black/20 focus:ring-4 focus:ring-black/5"
                placeholder="https://example.com/avatar.png"
              />
            </div>
          </FieldShell>

          <FieldShell label="加入时间">
            <ReadonlyField value={formatDate(settings.user.date_joined)} />
          </FieldShell>

          <div className="md:col-span-2">
            <FieldShell label="备注">
              <textarea
                value={profileForm.remark}
                onChange={(event) => setProfileForm((current) => ({ ...current, remark: event.target.value }))}
                className="h-28 w-full resize-none rounded-2xl border border-gray-100 bg-white px-4 py-3 text-sm font-medium text-gray-900 outline-none transition focus:border-black/20 focus:ring-4 focus:ring-black/5"
                placeholder="可填写个人简介、偏好说明等。"
              />
            </FieldShell>
          </div>
        </div>

        <div className="flex justify-end">
          <button
            type="button"
            onClick={handleSaveProfile}
            disabled={profileSaving}
            className="inline-flex h-12 items-center justify-center gap-2 rounded-2xl bg-black px-6 text-sm font-black text-white transition hover:-translate-y-0.5 disabled:cursor-not-allowed disabled:opacity-60"
          >
            {profileSaving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
            {profileSaving ? "保存中" : t.save}
          </button>
        </div>
      </motion.div>
    );
  };

  const renderSecurity = () => {
    if (!settings) return null;
    return (
      <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} className="space-y-8">
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
          {[
            {
              icon: BadgeCheck,
              label: "账户状态",
              value: settings.user.status,
              desc: "当前账户可用状态",
            },
            {
              icon: Clock3,
              label: "最近登录",
              value: formatDate(settings.user.last_login_at),
              desc: settings.user.last_login_ip ? `IP: ${settings.user.last_login_ip}` : "暂无登录 IP 记录",
            },
            {
              icon: Shield,
              label: "安全入口",
              value: "密码与验证",
              desc: "如需使用旧版安全页，也可跳转处理。",
            },
          ].map((item) => (
            <div key={item.label} className="rounded-[2rem] border border-gray-100 bg-white p-5 shadow-sm">
              <div className="flex items-center gap-3">
                <div className="flex h-11 w-11 items-center justify-center rounded-2xl bg-gray-50 text-gray-500">
                  <item.icon className="h-5 w-5" />
                </div>
                <div>
                  <p className="text-[10px] font-black uppercase tracking-[0.18em] text-gray-300">{item.label}</p>
                  <p className="mt-1 text-sm font-bold text-gray-900">{item.value}</p>
                </div>
              </div>
              <p className="mt-4 text-xs leading-5 text-gray-400">{item.desc}</p>
            </div>
          ))}
        </div>

        <div className="rounded-[2.5rem] border border-gray-100 bg-white p-6 shadow-sm">
          <div className="flex items-center gap-3">
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-black text-white shadow-lg shadow-black/10">
              <KeyRound className="h-5 w-5" />
            </div>
            <div>
              <h4 className="text-lg font-black tracking-tight text-gray-950">修改登录密码</h4>
              <p className="text-sm text-gray-400">这里直接调用已登录用户的密码修改接口，不再使用静态占位。</p>
            </div>
          </div>

          <div className="mt-6 grid grid-cols-1 gap-4 md:grid-cols-3">
            <FieldShell label="当前密码">
              <input
                type="password"
                value={passwordForm.current_password}
                onChange={(event) => setPasswordForm((current) => ({ ...current, current_password: event.target.value }))}
                className="w-full rounded-2xl border border-gray-100 bg-gray-50 px-4 py-3 text-sm font-medium text-gray-900 outline-none transition focus:border-black/20 focus:ring-4 focus:ring-black/5"
                placeholder="输入当前密码"
              />
            </FieldShell>

            <FieldShell label="新密码">
              <input
                type="password"
                value={passwordForm.new_password}
                onChange={(event) => setPasswordForm((current) => ({ ...current, new_password: event.target.value }))}
                className="w-full rounded-2xl border border-gray-100 bg-gray-50 px-4 py-3 text-sm font-medium text-gray-900 outline-none transition focus:border-black/20 focus:ring-4 focus:ring-black/5"
                placeholder="输入新密码"
              />
            </FieldShell>

            <FieldShell label="确认新密码">
              <input
                type="password"
                value={passwordForm.confirm_password}
                onChange={(event) => setPasswordForm((current) => ({ ...current, confirm_password: event.target.value }))}
                className="w-full rounded-2xl border border-gray-100 bg-gray-50 px-4 py-3 text-sm font-medium text-gray-900 outline-none transition focus:border-black/20 focus:ring-4 focus:ring-black/5"
                placeholder="再次输入新密码"
              />
            </FieldShell>
          </div>

          <div className="mt-6 flex flex-col gap-3 sm:flex-row sm:justify-end">
            <button
              type="button"
              onClick={handleChangePassword}
              disabled={passwordSaving}
              className="inline-flex h-11 items-center justify-center gap-2 rounded-2xl bg-black px-5 text-sm font-black text-white transition hover:-translate-y-0.5 disabled:cursor-not-allowed disabled:opacity-60"
            >
              {passwordSaving ? <Loader2 className="h-4 w-4 animate-spin" /> : <KeyRound className="h-4 w-4" />}
              {passwordSaving ? "更新中" : "更新密码"}
            </button>
          </div>
        </div>
      </motion.div>
    );
  };

  const renderSupport = () => {
    if (!settings) return null;
    return (
      <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} className="space-y-8">
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
          {[
            { label: "反馈账号", value: settings.user.username, desc: "提交问题时请附带账号，便于快速定位。" },
            { label: "联系邮箱", value: settings.user.email || "未设置", desc: "如需回信处理，请先确保邮箱填写正确。" },
            { label: "当前状态", value: settings.user.status, desc: "支持页已接入当前工作台，不再跳转旧静态页。" },
          ].map((item) => (
            <div key={item.label} className="rounded-[2rem] border border-gray-100 bg-white p-5 shadow-sm">
              <p className="text-[10px] font-black uppercase tracking-[0.18em] text-gray-300">{item.label}</p>
              <p className="mt-4 text-lg font-black tracking-tight text-gray-950">{item.value}</p>
              <p className="mt-3 text-xs leading-5 text-gray-400">{item.desc}</p>
            </div>
          ))}
        </div>

        <div className="rounded-[2.5rem] border border-gray-100 bg-white p-6 shadow-sm">
          <div className="flex items-center gap-3">
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-black text-white shadow-lg shadow-black/10">
              <Mail className="h-5 w-5" />
            </div>
            <div>
              <h4 className="text-lg font-black tracking-tight text-gray-950">支持反馈模板</h4>
              <p className="text-sm text-gray-400">复制下面的模板，把问题描述、复现步骤和实际结果补充完整后发送给管理员即可。</p>
            </div>
          </div>

          <div className="mt-6 overflow-hidden rounded-[1.75rem] border border-gray-100 bg-gray-50/80">
            <pre className="whitespace-pre-wrap p-5 text-sm leading-7 text-gray-700">{supportTemplate}</pre>
          </div>

          <div className="mt-6 flex flex-col gap-3 sm:flex-row sm:justify-end">
            <button
              type="button"
              onClick={() => setActiveTab("security")}
              className="inline-flex h-11 items-center justify-center rounded-2xl border border-gray-200 px-5 text-sm font-bold text-gray-600 transition hover:border-gray-300 hover:text-black"
            >
              前往安全中心
            </button>
            <button
              type="button"
              onClick={copySupportTemplate}
              className="inline-flex h-11 items-center justify-center gap-2 rounded-2xl bg-black px-5 text-sm font-black text-white transition hover:-translate-y-0.5"
            >
              <Copy className="h-4 w-4" />
              复制反馈模板
            </button>
          </div>
        </div>
      </motion.div>
    );
  };

  const renderQuota = () => {
    if (!settings) return null;
    const cards = [
      { label: "每日免费次数", value: settings.quota.daily_quota },
      { label: "当前可用次数", value: settings.quota.available_quota },
      { label: "冻结次数", value: settings.quota.frozen_quota },
      { label: "累计生成次数", value: settings.quota.total_generated_count },
    ];
    return (
      <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} className="space-y-8">
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
          {cards.map((item) => (
            <div key={item.label} className="rounded-[2rem] border border-gray-100 bg-white p-5 shadow-sm">
              <p className="text-[10px] font-black uppercase tracking-[0.18em] text-gray-300">{item.label}</p>
              <p className="mt-4 text-3xl font-black tracking-tight text-gray-950">{item.value}</p>
            </div>
          ))}
        </div>

        <div className="rounded-[2.25rem] border border-gray-100 bg-gray-50/70 p-6">
          <div className="flex items-center gap-3">
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-white text-gray-500 shadow-sm">
              <Sparkles className="h-5 w-5" />
            </div>
            <div>
              <h4 className="text-lg font-black tracking-tight text-gray-950">当前工作台已接入的真实配置</h4>
              <p className="text-sm text-gray-400">这部分只显示当前后端真实返回的数据；原先的静态主题、隐私、推送占位项已从设置页移除。</p>
            </div>
          </div>

          <div className="mt-6 grid grid-cols-1 gap-4 md:grid-cols-2">
            <FieldShell label="上次额度重置时间">
              <ReadonlyField value={formatDate(settings.quota.last_reset_date)} />
            </FieldShell>
            <FieldShell label="账户角色 / 状态">
              <ReadonlyField value={`${settings.user.role} / ${settings.user.status}`} />
            </FieldShell>
          </div>
        </div>
      </motion.div>
    );
  };

  const renderContent = () => {
    if (loading) {
      return (
        <div className="grid min-h-[360px] place-items-center">
          <div className="flex items-center gap-3 text-sm font-bold text-gray-500">
            <Loader2 className="h-5 w-5 animate-spin" />
            正在读取账户设置
          </div>
        </div>
      );
    }

    if (!settings) {
      return (
        <div className="flex min-h-[360px] flex-col items-center justify-center gap-4 text-center">
          <p className="text-sm font-bold text-gray-500">设置数据加载失败。</p>
          <button
            type="button"
            onClick={loadSettings}
            className="inline-flex h-11 items-center justify-center rounded-2xl bg-black px-5 text-sm font-black text-white"
          >
            重试
          </button>
        </div>
      );
    }

    if (activeTab === "security") return renderSecurity();
    if (activeTab === "quota") return renderQuota();
    if (activeTab === "support") return renderSupport();
    return renderProfile();
  };

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/40 p-0 backdrop-blur-sm sm:p-4"
    >
      <motion.div
        initial={{ scale: 0.96, opacity: 0, y: 20 }}
        animate={{ scale: 1, opacity: 1, y: 0 }}
        exit={{ scale: 0.97, opacity: 0, y: 20 }}
        transition={{ type: "spring", damping: 26, stiffness: 280 }}
        className="flex h-[100vh] w-full max-w-6xl flex-col overflow-hidden bg-white shadow-2xl sm:h-[90vh] sm:rounded-[2rem] md:h-[84vh] md:flex-row md:rounded-[3rem]"
      >
        <aside className="w-full shrink-0 border-b border-gray-100 bg-gray-50/40 p-5 md:w-80 md:border-b-0 md:border-r md:p-10">
          <div className="space-y-1 pr-12 md:pr-0">
            <h2 className="text-2xl font-black tracking-tight text-gray-950">{t.title}</h2>
            <p className="text-[10px] font-black uppercase tracking-[0.3em] text-gray-300">{t.subtitle}</p>
          </div>

          <nav className="mt-6 flex gap-3 overflow-x-auto pb-1 md:mt-10 md:block md:space-y-3 md:overflow-visible">
            {tabs.map((tab) => (
              <button
                key={tab.id}
                type="button"
                onClick={() => setActiveTab(tab.id)}
                className={`flex min-w-[220px] items-center justify-between rounded-2xl px-4 py-3 text-left transition md:min-w-0 md:w-full ${
                  activeTab === tab.id
                    ? "border border-gray-100 bg-white text-black shadow-sm"
                    : "text-gray-400 hover:bg-white/70 hover:text-gray-800"
                }`}
              >
                <div className="flex items-center gap-3">
                  <div className={`flex h-10 w-10 items-center justify-center rounded-2xl ${activeTab === tab.id ? "bg-black text-white" : "bg-white text-gray-400"}`}>
                    <tab.icon className="h-4 w-4" />
                  </div>
                  <div>
                    <div className="text-sm font-bold">{tab.label}</div>
                    <div className="text-[9px] font-black uppercase tracking-[0.18em] text-gray-300">{tab.tag}</div>
                  </div>
                </div>
              </button>
            ))}
          </nav>
        </aside>

        <section className="relative flex-1 overflow-hidden bg-white">
          <button
            type="button"
            onClick={onClose}
            className="absolute right-4 top-4 z-20 rounded-2xl p-3 text-gray-400 transition-all hover:bg-gray-50 hover:text-black sm:right-6 sm:top-6"
          >
            <X className="h-5 w-5" />
          </button>

          <div className="h-full overflow-y-auto p-5 pt-16 sm:p-8 sm:pt-20 md:p-16 md:pt-20">
            <div className="mx-auto max-w-4xl space-y-6">
              {notice ? <Notice tone={notice.tone} message={notice.message} /> : null}
              <AnimatePresence mode="wait">
                <div key={activeTab}>{renderContent()}</div>
              </AnimatePresence>
            </div>
          </div>
        </section>
      </motion.div>
    </motion.div>
  );
}
