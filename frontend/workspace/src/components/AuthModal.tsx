import { FormEvent, useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { ArrowRight, KeyRound, Mail, RefreshCw, Send, Sparkles, User, X, type LucideIcon } from "lucide-react";
import {
  checkRegisterAvailability,
  fetchLoginCaptcha,
  loginAccount,
  registerAccount,
  sendRegisterEmailCode,
  type UserInfo,
} from "../services/apiService";

type AuthMode = "login" | "register";

interface AuthModalProps {
  initialMode: AuthMode;
  initialCaptcha?: string;
  initialCaptchaSvg?: string;
  onClose: () => void;
  onAuthenticated: (user: UserInfo) => void;
  addToast: (message: string, type?: "success" | "error" | "info") => void;
}

function readableError(error: unknown): string {
  const message = error instanceof Error ? error.message : "操作失败，请稍后再试";
  try {
    const parsed = JSON.parse(message);
    const firstKey = Object.keys(parsed)[0];
    const firstError = parsed[firstKey]?.[0]?.message;
    return firstError || message;
  } catch {
    return message;
  }
}

function AuthField({
  icon: Icon,
  label,
  value,
  onChange,
  type = "text",
  placeholder,
  autoComplete,
  inputMode,
  maxLength,
  onBlur,
  hint,
  hintTone = "info",
}: {
  icon: LucideIcon;
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: string;
  placeholder?: string;
  autoComplete?: string;
  inputMode?: "text" | "email" | "numeric";
  maxLength?: number;
  onBlur?: () => void;
  hint?: string;
  hintTone?: "info" | "success" | "error";
}) {
  return (
    <label className="block space-y-2">
      <span className="pl-2 text-xs font-black tracking-wide text-slate-300">{label}</span>
      <div className="flex h-11 items-center gap-3 rounded-[1.15rem] border border-slate-200/70 bg-slate-50/65 px-3.5 transition focus-within:border-emerald-300 focus-within:bg-white focus-within:shadow-[0_14px_32px_rgba(15,23,42,0.08)] sm:h-12 sm:rounded-[1.35rem] sm:px-4">
        <Icon className="h-4 w-4 shrink-0 text-slate-400 sm:h-[18px] sm:w-[18px]" />
        <input
          value={value}
          onChange={(event) => onChange(event.target.value)}
          onBlur={onBlur}
          className="h-full min-w-0 flex-1 bg-transparent text-sm font-semibold text-slate-700 outline-none placeholder:text-slate-400 sm:text-[15px]"
          type={type}
          placeholder={placeholder}
          autoComplete={autoComplete}
          inputMode={inputMode}
          maxLength={maxLength}
          required
        />
      </div>
      {hint ? (
        <p
          className={`pl-2 text-xs ${
            hintTone === "error" ? "text-red-500" : hintTone === "success" ? "text-emerald-600" : "text-slate-400"
          }`}
        >
          {hint}
        </p>
      ) : null}
    </label>
  );
}

export default function AuthModal({
  initialMode,
  initialCaptcha = "",
  initialCaptchaSvg = "",
  onClose,
  onAuthenticated,
  addToast,
}: AuthModalProps) {
  const [mode, setMode] = useState<AuthMode>(initialMode);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [codeStatus, setCodeStatus] = useState("");
  const [codeTone, setCodeTone] = useState<"info" | "success" | "error">("info");
  const [cooldown, setCooldown] = useState(0);
  const [usernameStatus, setUsernameStatus] = useState("");
  const [usernameTone, setUsernameTone] = useState<"info" | "success" | "error">("info");
  const [captcha, setCaptcha] = useState(initialCaptcha);
  const [captchaSvg, setCaptchaSvg] = useState(initialCaptchaSvg);
  const [loginForm, setLoginForm] = useState({
    username: "",
    password: "",
    captcha_code: "",
  });
  const [registerForm, setRegisterForm] = useState({
    username: "",
    email: "",
    email_verification_code: "",
    password: "",
    confirm_password: "",
  });

  useEffect(() => {
    setMode(initialMode);
  }, [initialMode]);

  useEffect(() => {
    if (mode !== "login" || captcha) return;
    fetchLoginCaptcha()
      .then((data) => {
        setCaptcha(data.captcha_code);
        setCaptchaSvg(data.captcha_svg);
      })
      .catch(() => {});
  }, [mode, captcha]);

  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = window.setTimeout(() => setCooldown((value) => value - 1), 1000);
    return () => window.clearTimeout(timer);
  }, [cooldown]);

  const refreshCaptcha = async () => {
    try {
      const data = await fetchLoginCaptcha();
      setCaptcha(data.captcha_code);
      setCaptchaSvg(data.captcha_svg);
      setLoginForm((current) => ({ ...current, captcha_code: "" }));
    } catch {
      addToast("验证码刷新失败，请稍后再试", "error");
    }
  };

  const handleSendCode = async () => {
    const email = registerForm.email.trim();
    const username = registerForm.username.trim();
    if (!username) {
      setUsernameTone("error");
      setUsernameStatus("请先填写账号");
      return;
    }
    if (!email) {
      setCodeTone("error");
      setCodeStatus("请先填写邮箱地址");
      return;
    }

    setCodeTone("info");
    setCodeStatus("正在发送验证码...");
    try {
      const data = await sendRegisterEmailCode(email, username);
      setUsernameTone("success");
      setUsernameStatus("账号可用");
      setCodeTone("success");
      setCodeStatus(`验证码已发送，${Math.ceil(data.expire_seconds / 60)} 分钟内有效。`);
      setCooldown(data.resend_seconds || 60);
    } catch (error) {
      const message = readableError(error);
      if (message.includes("账号")) {
        setUsernameTone("error");
        setUsernameStatus(message);
      }
      setCodeTone("error");
      setCodeStatus(message);
    }
  };

  const handleRegisterUsernameBlur = async () => {
    const username = registerForm.username.trim();
    if (!username) {
      setUsernameStatus("");
      return;
    }
    try {
      const data = await checkRegisterAvailability({ username });
      if (data.username) {
        setUsernameTone(data.username.available ? "success" : "error");
        setUsernameStatus(data.username.message);
      }
    } catch (error) {
      setUsernameTone("error");
      setUsernameStatus(readableError(error));
    }
  };

  const finishAuthenticated = (username: string) => {
    onAuthenticated({
      username,
      nickname: "",
      display_name: username,
      email: registerForm.email,
      avatar: "",
      remark: "",
      role: "用户",
      daily_quota: 0,
      quota_remaining: 0,
      quota_frozen: 0,
      quota_total: 0,
    });
  };

  const handleRegister = async (event: FormEvent) => {
    event.preventDefault();
    setIsSubmitting(true);
    try {
      const data = await registerAccount(registerForm);
      finishAuthenticated(data.username || registerForm.username);
      addToast("注册成功，欢迎进入创作台", "success");
    } catch (error) {
      addToast(readableError(error), "error");
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleLogin = async (event: FormEvent) => {
    event.preventDefault();
    setIsSubmitting(true);
    try {
      const data = await loginAccount(loginForm);
      finishAuthenticated(data.username || loginForm.username);
      addToast("登录成功", "success");
    } catch (error) {
      addToast(readableError(error), "error");
      refreshCaptcha();
    } finally {
      setIsSubmitting(false);
    }
  };

  const registerReady = Boolean(
    registerForm.username.trim() &&
    registerForm.email.trim() &&
    registerForm.email_verification_code.trim() &&
    registerForm.password &&
    registerForm.confirm_password
  );

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-[180] flex items-center justify-center overflow-hidden bg-slate-950/25 p-4 backdrop-blur-xl"
    >
      <div className="pointer-events-none absolute left-[18%] top-[16%] h-28 w-28 rounded-full bg-emerald-300/35 blur-3xl" />
      <div className="pointer-events-none absolute right-[23%] top-[7%] h-24 w-24 rounded-full bg-amber-200/65 blur-3xl" />
      <div className="pointer-events-none absolute bottom-[8%] left-[27%] h-32 w-32 rounded-full bg-sky-200/45 blur-3xl" />

      <motion.div
        initial={{ opacity: 0, y: 20, scale: 0.96 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: 20, scale: 0.96 }}
        className="relative w-full max-w-[540px] overflow-hidden rounded-[2rem] border border-white/80 bg-white/90 shadow-[0_32px_82px_rgba(15,23,42,0.22)] backdrop-blur-2xl md:rounded-[2.8rem]"
      >
        <button
          onClick={onClose}
          className="absolute right-4 top-4 z-20 cursor-pointer rounded-full p-2 text-slate-400 transition-colors hover:bg-slate-100 hover:text-slate-900 sm:right-5 sm:top-5"
          aria-label="关闭"
        >
          <X className="h-5 w-5" />
        </button>

        <div className="max-h-[calc(100vh-1rem)] overflow-y-auto px-5 py-5 sm:max-h-[calc(100vh-2rem)] sm:px-9 sm:py-7 md:px-10 md:py-8">
          <div className="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-[1.15rem] bg-white shadow-[0_14px_36px_rgba(15,23,42,0.08)] sm:mb-4 sm:h-14 sm:w-14 sm:rounded-[1.25rem]">
            <Sparkles className="h-7 w-7 text-emerald-500 sm:h-8 sm:w-8" />
          </div>

          <div className="mb-4 text-center sm:mb-5">
            <h3 className="text-[1.55rem] font-black tracking-tight text-slate-950 sm:text-[1.9rem] md:text-[2.25rem]">
              {mode === "register" ? "加入镜界" : "欢迎回到镜界"}
            </h3>
            <p className="mt-1.5 text-[13px] leading-5 text-slate-400 sm:mt-2 sm:text-sm sm:leading-6 md:text-[15px]">
              {mode === "register"
                ? "创建账号，开启你的无限创意映射旅程"
                : "登录以继续你的无限创意映射旅程"}
            </p>
          </div>

          <div className="mb-4 grid grid-cols-2 gap-2 rounded-[1.1rem] bg-slate-100/70 p-1.5 sm:mb-5 sm:rounded-[1.25rem]">
            <button
              type="button"
              onClick={() => setMode("login")}
              className={`h-10 cursor-pointer rounded-[0.95rem] px-3 text-[13px] font-black tracking-[0.14em] transition-all sm:h-11 sm:rounded-[1rem] sm:px-4 sm:text-sm sm:tracking-[0.18em] ${
                mode === "login"
                  ? "bg-white text-slate-950 shadow-[0_8px_22px_rgba(15,23,42,0.12)]"
                  : "text-slate-400 hover:text-slate-700"
              }`}
            >
              SIGN IN
            </button>
            <button
              type="button"
              onClick={() => setMode("register")}
              className={`h-10 cursor-pointer rounded-[0.95rem] px-3 text-[13px] font-black tracking-[0.14em] transition-all sm:h-11 sm:rounded-[1rem] sm:px-4 sm:text-sm sm:tracking-[0.18em] ${
                mode === "register"
                  ? "bg-white text-slate-950 shadow-[0_8px_22px_rgba(15,23,42,0.12)]"
                  : "text-slate-400 hover:text-slate-700"
              }`}
            >
              SIGN UP
            </button>
          </div>

          <AnimatePresence mode="wait">
            {mode === "register" ? (
              <motion.form
                key="register"
                initial={{ opacity: 0, x: 12 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: -12 }}
                onSubmit={handleRegister}
                className="space-y-3"
              >
                <AuthField
                  icon={User}
                  label="账号"
                  value={registerForm.username}
                  onChange={(value) => {
                    setRegisterForm((current) => ({ ...current, username: value }));
                    setUsernameStatus("");
                  }}
                  onBlur={handleRegisterUsernameBlur}
                  placeholder="选择你的账号名"
                  autoComplete="username"
                  hint={usernameStatus}
                  hintTone={usernameTone}
                />

                <AuthField
                  icon={Mail}
                  label="电子邮箱"
                  value={registerForm.email}
                  onChange={(value) => setRegisterForm((current) => ({ ...current, email: value }))}
                  type="email"
                  placeholder="name@example.com"
                  autoComplete="email"
                  inputMode="email"
                />

                <label className="block space-y-2">
                  <span className="pl-2 text-xs font-black tracking-wide text-slate-300">邮箱验证码</span>
                  <div className="grid gap-2.5 grid-cols-[1fr_112px] sm:gap-3 sm:grid-cols-[1fr_128px]">
                    <div className="flex h-11 items-center gap-3 rounded-[1.15rem] border border-slate-200/70 bg-slate-50/65 px-3.5 transition focus-within:border-emerald-300 focus-within:bg-white focus-within:shadow-[0_14px_32px_rgba(15,23,42,0.08)] sm:h-12 sm:rounded-[1.35rem] sm:px-4">
                      <Send className="h-4 w-4 shrink-0 text-slate-400 sm:h-[18px] sm:w-[18px]" />
                      <input
                        value={registerForm.email_verification_code}
                        onChange={(event) =>
                          setRegisterForm((current) => ({
                            ...current,
                            email_verification_code: event.target.value,
                          }))
                        }
                        className="h-full min-w-0 flex-1 bg-transparent text-sm font-semibold text-slate-700 outline-none placeholder:text-slate-400 sm:text-[15px]"
                        inputMode="numeric"
                        placeholder="6 位验证码"
                        required
                      />
                    </div>
                    <button
                      type="button"
                      onClick={handleSendCode}
                      disabled={cooldown > 0}
                      className="h-11 cursor-pointer rounded-[1.15rem] border border-slate-200/80 bg-white px-3 text-[13px] font-black text-slate-950 shadow-sm transition hover:border-slate-950 disabled:cursor-not-allowed disabled:bg-slate-50 disabled:text-slate-400 sm:h-12 sm:rounded-[1.35rem] sm:px-4 sm:text-sm"
                    >
                      {cooldown > 0 ? `${cooldown}s` : "发送验证码"}
                    </button>
                  </div>
                  {codeStatus && (
                    <p
                      className={`pl-2 text-xs ${
                        codeTone === "error"
                          ? "text-red-500"
                          : codeTone === "success"
                            ? "text-emerald-600"
                            : "text-slate-400"
                      }`}
                    >
                      {codeStatus}
                    </p>
                  )}
                </label>

                <AuthField
                  icon={KeyRound}
                  label="密码"
                  value={registerForm.password}
                  onChange={(value) => setRegisterForm((current) => ({ ...current, password: value }))}
                  type="password"
                  placeholder="至少 8 位"
                  autoComplete="new-password"
                />

                <AuthField
                  icon={KeyRound}
                  label="确认密码"
                  value={registerForm.confirm_password}
                  onChange={(value) => setRegisterForm((current) => ({ ...current, confirm_password: value }))}
                  type="password"
                  placeholder="再次输入密码"
                  autoComplete="new-password"
                />

                <button
                  type="submit"
                  disabled={isSubmitting || !registerReady}
                  className="mt-1 flex h-12 w-full cursor-pointer items-center justify-center gap-3 rounded-[1.2rem] bg-black px-5 text-[15px] font-black text-white shadow-[0_16px_34px_rgba(0,0,0,0.2)] transition hover:-translate-y-0.5 disabled:cursor-not-allowed disabled:bg-slate-200 disabled:text-slate-400 disabled:shadow-none sm:h-[52px] sm:rounded-[1.4rem] sm:text-base"
                >
                  <span>{isSubmitting ? "正在创建账号..." : "立即注册"}</span>
                  <ArrowRight className="h-4 w-4" />
                </button>
              </motion.form>
            ) : (
              <motion.form
                key="login"
                initial={{ opacity: 0, x: 12 }}
                animate={{ opacity: 1, x: 0 }}
                exit={{ opacity: 0, x: -12 }}
                onSubmit={handleLogin}
                className="space-y-3"
              >
                <AuthField
                  icon={Mail}
                  label="账号"
                  value={loginForm.username}
                  onChange={(value) => setLoginForm((current) => ({ ...current, username: value }))}
                  placeholder="输入你的账号"
                  autoComplete="username"
                />

                <AuthField
                  icon={KeyRound}
                  label="密码"
                  value={loginForm.password}
                  onChange={(value) => setLoginForm((current) => ({ ...current, password: value }))}
                  type="password"
                  placeholder="输入密码"
                  autoComplete="current-password"
                />

                <label className="block space-y-2">
                  <span className="pl-2 text-xs font-black tracking-wide text-slate-300">验证码</span>
                  <div className="grid gap-2.5 sm:grid-cols-[156px_1fr] sm:gap-3">
                    <div className="grid grid-cols-[1fr_88px] gap-2 sm:grid-cols-1 sm:gap-2">
                      <div className="relative flex h-11 items-center justify-center overflow-hidden rounded-[1.15rem] border border-slate-200/80 bg-[linear-gradient(135deg,#f8fbff_0%,#eef4ff_100%)] px-2 shadow-[inset_0_1px_0_rgba(255,255,255,0.85)] sm:h-12 sm:rounded-[1.35rem]">
                        <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(191,219,254,0.42),transparent_42%),radial-gradient(circle_at_bottom_right,rgba(196,181,253,0.18),transparent_40%)]" />
                        {captchaSvg ? (
                          <img
                            src={captchaSvg}
                            alt="验证码图片"
                            className="relative block h-8 w-full rounded-lg object-cover sm:h-9 sm:rounded-xl"
                          />
                        ) : (
                          <span className="relative font-mono text-sm font-black tracking-[0.2em] text-slate-700 sm:text-base sm:tracking-[0.24em]">
                            {captcha || "----"}
                          </span>
                        )}
                      </div>
                      <button
                        type="button"
                        onClick={refreshCaptcha}
                        className="inline-flex h-11 w-full cursor-pointer items-center justify-center gap-1.5 rounded-[1.15rem] border border-slate-200/80 bg-white px-2 text-[11px] font-bold text-slate-500 shadow-sm transition hover:border-slate-300 hover:text-slate-900 sm:h-8 sm:rounded-full sm:px-0"
                        title="刷新验证码"
                      >
                        <RefreshCw className="h-[13px] w-[13px] sm:h-[14px] sm:w-[14px]" />
                        <span className="hidden sm:inline">刷新验证码</span>
                        <span className="sm:hidden">刷新</span>
                      </button>
                    </div>
                    <input
                      value={loginForm.captcha_code}
                      onChange={(event) =>
                        setLoginForm((current) => ({ ...current, captcha_code: event.target.value }))
                      }
                      className="h-11 w-full rounded-[1.15rem] border border-slate-200/70 bg-slate-50/65 px-3.5 text-sm font-semibold uppercase text-slate-700 outline-none transition placeholder:text-slate-400 focus:border-emerald-300 focus:bg-white focus:shadow-[0_14px_32px_rgba(15,23,42,0.08)] sm:h-12 sm:rounded-[1.35rem] sm:px-4 sm:text-[15px]"
                      maxLength={8}
                      placeholder="输入验证码"
                      required
                    />
                  </div>
                  <div className="flex flex-wrap justify-between gap-1 pl-1 text-[11px] text-slate-400">
                    <span>不区分大小写</span>
                    <span>点击刷新按钮可更换</span>
                  </div>
                  <div className="flex justify-end">
                    <a
                      href="/forgot-password"
                      className="text-xs font-bold text-slate-400 transition hover:text-slate-950"
                    >
                      忘记密码
                    </a>
                  </div>
                </label>
                <button
                  type="submit"
                  disabled={isSubmitting}
                  className="mt-1 flex h-12 w-full cursor-pointer items-center justify-center gap-3 rounded-[1.2rem] bg-black px-5 text-[15px] font-black text-white shadow-[0_16px_34px_rgba(0,0,0,0.2)] transition hover:-translate-y-0.5 disabled:cursor-not-allowed disabled:bg-slate-200 disabled:text-slate-400 disabled:shadow-none sm:h-[52px] sm:rounded-[1.4rem] sm:text-base"
                >
                  <span>{isSubmitting ? "正在登录..." : "立即登录"}</span>
                  <ArrowRight className="h-4 w-4" />
                </button>
              </motion.form>
            )}
          </AnimatePresence>

          <p className="mt-3 text-center text-[10px] font-medium leading-5 text-slate-300 sm:text-[11px]">
            点击继续即代表你同意我们的《服务协议》与《隐私政策》
          </p>
        </div>
      </motion.div>
    </motion.div>
  );
}
