import { useState } from "react";
import { motion, AnimatePresence } from "motion/react";
import { 
  X, User, Shield, Globe, Bell, Palette, 
  Smartphone, Laptop, Fingerprint, Cpu, 
  Trash2, Download, Info, Check, ChevronRight,
  Sun, Moon, Monitor, Wand2, Mail, Lock
} from "lucide-react";

export default function SettingsModal({ 
  onClose,
  t
}: { 
  onClose: () => void,
  t: any
}) {
  const [activeTab, setActiveTab] = useState("profile");
  const [activeSection, setActiveSection] = useState(0);

  const sections = [
    { 
      id: "account", 
      title: t.tabs.account, 
      icon: User, 
      items: [
        { id: "profile", label: t.labels.profile, tag: "PROFILE" },
        { id: "security", label: t.labels.security, tag: "SECURITY" },
        { id: "billing", label: t.labels.billing, tag: "FINANCE" }
      ] 
    },
    { 
      id: "pref", 
      title: t.tabs.pref, 
      icon: Palette, 
      items: [
        { id: "ui", label: t.labels.ui, tag: "UI" },
        { id: "language", label: t.labels.language, tag: "LOCALE" },
        { id: "ai", label: t.labels.ai, tag: "ENGINE" }
      ] 
    },
    { 
      id: "notify", 
      title: t.tabs.notify, 
      icon: Bell, 
      items: [
        { id: "notif_push", label: t.labels.notif_push, tag: "PUSH" },
        { id: "privacy", label: t.labels.privacy, tag: "PRIVACY" },
        { id: "data", label: t.labels.data, tag: "DATA" }
      ] 
    }
  ];

  const renderContent = () => {
    switch (activeTab) {
      case "profile":
        return (
          <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} className="space-y-10">
            <header className="flex items-center gap-8 border-b border-gray-100 pb-10">
              <div className="relative group cursor-pointer">
                <div className="w-24 h-24 rounded-[2.5rem] bg-gray-100 border-4 border-white shadow-xl overflow-hidden">
                  <img src="https://api.dicebear.com/7.x/avataaars/svg?seed=admin" alt="Avatar" className="w-full h-full" />
                </div>
                <div className="absolute inset-0 bg-black/40 rounded-[2.5rem] flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity">
                  <span className="text-[10px] text-white font-bold tracking-widest uppercase italic">更换头像</span>
                </div>
              </div>
              <div className="space-y-1">
                <h3 className="text-2xl font-bold text-gray-900 leading-none">admin</h3>
                <p className="text-sm text-gray-400 font-medium lowercase">admin@gmail.com</p>
                <div className="flex gap-2 mt-3">
                  <span className="px-3 py-1 bg-black text-white text-[9px] font-black italic rounded-full uppercase tracking-widest">镜界创造者</span>
                  <span className="px-3 py-1 bg-gray-100 text-gray-400 text-[9px] font-black italic rounded-full uppercase tracking-widest">UID: 88721</span>
                </div>
              </div>
            </header>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-x-12 gap-y-8">
              {[
                { label: "昵称 (Display Name)", value: "admin", type: "text" },
                { label: "电子邮箱 (Email Address)", value: "admin@gmail.com", type: "email" },
                { label: "个人简介 (Bio)", value: "致力于探索 AI 与艺术的边界。", type: "area" },
                { label: "绑定地区 (Region)", value: "中国, 北京", type: "text" }
              ].map((field, i) => (
                <div key={i} className={`space-y-2 ${field.type === 'area' ? 'md:col-span-2' : ''}`}>
                  <label className="text-[10px] font-black text-gray-400 uppercase tracking-[0.2em]">{field.label}</label>
                  {field.type === 'area' ? (
                    <textarea defaultValue={field.value} className="w-full h-24 bg-gray-50 border border-gray-100 rounded-2xl p-4 text-sm font-medium focus:ring-2 focus:ring-black/5 outline-none transition-all resize-none text-gray-900" />
                  ) : (
                    <input type={field.type} defaultValue={field.value} className="w-full bg-gray-50 border border-gray-100 rounded-2xl p-4 text-sm font-medium focus:ring-2 focus:ring-black/5 outline-none transition-all text-gray-900" />
                  )}
                </div>
              ))}
            </div>

            <div className="pt-6">
              <button className="px-10 py-4 bg-black text-white rounded-2xl text-xs font-bold hover:scale-105 active:scale-95 transition-all shadow-xl shadow-black/10">{t.save}</button>
            </div>
          </motion.div>
        );
      
      case "ai":
        return (
          <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} className="space-y-12">
            <section className="space-y-6">
              <div className="flex items-center gap-3">
                <Cpu className="w-5 h-5 text-gray-400" />
                <h4 className="font-bold text-lg">核心引擎配置</h4>
              </div>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {[
                  { label: "后台 LLM 对接方", version: "Console Managed", desc: "工作台对话模型由对接方配置提供。", active: true },
                  { label: "生图模型独立配置", version: "Image Only", desc: "生图模型只用于生成任务，不参与对话。", active: false }
                ].map((model, i) => (
                  <div key={i} className={`p-6 rounded-[2rem] border-2 transition-all cursor-pointer ${model.active ? 'border-black bg-white shadow-xl' : 'border-gray-100 grayscale opacity-50'}`}>
                    <div className="flex justify-between items-start mb-4">
                      <span className="text-sm font-black uppercase tracking-widest">{model.label}</span>
                      {model.active && <Check className="w-4 h-4 text-emerald-500" />}
                    </div>
                    <p className="text-xs text-gray-400 mb-2">{model.desc}</p>
                    <span className="text-[9px] font-bold text-gray-300 italic">{model.version}</span>
                  </div>
                ))}
              </div>
            </section>

            <section className="space-y-8 bg-gray-50/50 p-8 rounded-[2.5rem] border border-gray-100">
              <div className="space-y-6">
                {[
                  { label: "创作发散度 (Temperature)", value: 0.7, hint: "值越高越具有创造力，越低越严谨。" },
                  { label: "Top-P (Nucleus Sampling)", value: 0.9, hint: "控制生成词汇的相关性概率。" }
                ].map((slider, i) => (
                  <div key={i} className="space-y-3">
                    <div className="flex justify-between items-end">
                      <div className="space-y-1">
                        <p className="text-xs font-bold text-gray-800">{slider.label}</p>
                        <p className="text-[10px] text-gray-400 italic">{slider.hint}</p>
                      </div>
                      <span className="text-xs font-black font-mono">{(slider.value * 10).toFixed(0)} / 10</span>
                    </div>
                    <div className="relative h-1.5 w-full bg-gray-200 rounded-full">
                      <div className="absolute left-0 top-0 h-full bg-black rounded-full" style={{ width: `${slider.value * 100}%` }} />
                      <div className="absolute top-1/2 -translate-y-1/2 w-4 h-4 bg-white border-2 border-black rounded-full shadow-lg" style={{ left: `calc(${slider.value * 100}% - 8px)` }} />
                    </div>
                  </div>
                ))}
              </div>
            </section>
          </motion.div>
        );

      case "ui":
        return (
          <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} className="space-y-10">
            <section className="space-y-8">
              <div className="flex items-center gap-3">
                <Sun className="w-5 h-5 text-gray-400" />
                <h4 className="font-bold text-lg">显示与外观</h4>
              </div>
              <div className="grid grid-cols-1 gap-6">
                {[
                  { icon: Sun, label: "浅色模式", id: "light", active: true },
                ].map((theme, i) => (
                  <div 
                    key={i} 
                    className={`p-8 rounded-[2rem] border-2 flex flex-col items-center gap-4 transition-all cursor-pointer ${theme.active ? 'border-black bg-white shadow-xl' : 'border-gray-50 bg-gray-50/50 grayscale opacity-50'}`}
                  >
                    <theme.icon className={`w-6 h-6 ${theme.active ? 'text-black' : 'text-gray-300'}`} />
                    <span className={`text-[10px] font-black uppercase tracking-widest ${theme.active ? 'text-black' : 'text-gray-400'}`}>{theme.label}</span>
                  </div>
                ))}
              </div>
            </section>

            <section className="p-8 bg-black rounded-[2.5rem] text-white space-y-6">
              <div className="flex justify-between items-center">
                <div className="space-y-1">
                  <p className="font-bold italic">镜界灵感注入</p>
                  <p className="text-xs text-gray-500">每次进入应用时，自动为您推送最新的艺术灵感。</p>
                </div>
                <div className="w-12 h-6 bg-emerald-500 rounded-full p-1 flex items-center justify-end">
                  <div className="w-4 h-4 bg-white rounded-full shadow-lg" />
                </div>
              </div>
            </section>
          </motion.div>
        );

      case "security":
        return (
          <motion.div initial={{ opacity: 0, y: 10 }} animate={{ opacity: 1, y: 0 }} className="space-y-10">
             <div className="space-y-6">
                {[
                  { icon: Fingerprint, label: "生物识别验证 (Bio-Auth)", desc: "开启后，大额充值与敏感操作需要验证指纹或面部识别。", status: "未启用" },
                  { icon: Lock, label: "两步验证 (2FA)", desc: "通过 Google Authenticator 保护您的账户安全。", status: "已开启" },
                  { icon: Smartphone, label: "信任设备列表", desc: "查看当前所有已登录绘镜的设备终端。", status: "3 台设备" }
                ].map((item, i) => (
                  <div key={i} className="flex items-center justify-between p-6 bg-white rounded-[2rem] border border-gray-100 shadow-sm hover:border-black/10 transition-all cursor-pointer group">
                    <div className="flex items-center gap-5">
                      <div className="w-12 h-12 rounded-2xl bg-gray-50 flex items-center justify-center group-hover:bg-black group-hover:text-white transition-colors">
                        <item.icon className="w-5 h-5" />
                      </div>
                      <div className="space-y-1">
                        <p className="text-sm font-bold text-gray-900">{item.label}</p>
                        <p className="text-xs text-gray-400 italic">{item.desc}</p>
                      </div>
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="text-[10px] font-black text-gray-300 uppercase tracking-widest">{item.status}</span>
                      <ChevronRight className="w-4 h-4 text-gray-200" />
                    </div>
                  </div>
                ))}
             </div>

             <section className="p-8 border-2 border-red-50 border-dashed rounded-[2.5rem] space-y-4">
                <div className="flex items-center gap-2 text-red-500">
                  <Trash2 className="w-4 h-4" />
                  <span className="text-xs font-black uppercase tracking-widest leading-none">危险区域 (Danger Zone)</span>
                </div>
                <div className="flex justify-between items-center">
                  <p className="text-xs text-gray-400">注销您的所有数据并永久关闭账户。此操作不可撤销。</p>
                  <button className="px-6 py-2 bg-red-50 text-red-500 text-[10px] font-bold rounded-xl hover:bg-red-500 hover:text-white transition-all">彻底停用</button>
                </div>
             </section>
          </motion.div>
        );

      default:
        return (
          <div className="h-full flex flex-col items-center justify-center text-center space-y-4">
            <div className="w-20 h-20 bg-gray-50 rounded-full flex items-center justify-center">
              <Info className="w-8 h-8 text-gray-200" />
            </div>
            <div className="space-y-1">
              <h3 className="font-bold text-gray-400 leading-none italic uppercase tracking-widest">即将上线</h3>
              <p className="text-xs text-gray-300 italic">绘镜正在努力完善此功能...</p>
            </div>
          </div>
        );
    }
  };

  return (
    <motion.div 
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-[100] flex items-center justify-center p-4 bg-black/40 backdrop-blur-sm"
    >
      <motion.div 
        initial={{ scale: 0.95, opacity: 0, x: 50 }}
        animate={{ scale: 1, opacity: 1, x: 0 }}
        exit={{ scale: 0.95, opacity: 0, x: 50 }}
        transition={{ type: "spring", damping: 30, stiffness: 300 }}
        className="bg-white w-full max-w-6xl h-[90vh] md:h-[80vh] rounded-[2rem] md:rounded-[3.5rem] shadow-2xl flex flex-col md:flex-row overflow-hidden neumorph-subtle"
      >
        {/* Sidebar Nav */}
        <div className="w-full md:w-80 border-b md:border-b-0 md:border-r border-gray-100 bg-gray-50/40 p-6 md:p-12 space-y-10 overflow-hidden shrink-0">
          <div className="space-y-1 shrink-0">
            <h2 className="text-2xl font-black tracking-tighter">{t.title}</h2>
            <p className="text-[10px] text-gray-400 font-black uppercase tracking-[0.3em] italic">{t.subtitle}</p>
          </div>
          
          <nav className="space-y-10 overflow-y-auto">
            {sections.map((section, idx) => (
              <div key={idx} className="space-y-4">
                <div className="flex items-center gap-2 px-2">
                  <div className="w-1 h-3 bg-black rounded-full" />
                  <span className="text-[10px] font-black text-gray-300 uppercase tracking-widest">{section.title}</span>
                </div>
                <div className="space-y-1">
                  {section.items.map((item) => (
                    <button 
                      key={item.id}
                      onClick={() => setActiveTab(item.id)}
                      className={`w-full group flex items-center justify-between px-4 py-3 rounded-2xl transition-all ${activeTab === item.id ? 'bg-white border border-gray-100 shadow-sm text-black font-bold' : 'text-gray-400 hover:text-gray-900 hover:bg-gray-100/50'}`}
                    >
                      <span className="text-sm">{item.label}</span>
                      <span className={`text-[8px] font-black italic tracking-widest group-hover:opacity-100 transition-opacity ${activeTab === item.id ? 'opacity-100 text-gray-300' : 'opacity-0'}`}>{item.tag}</span>
                    </button>
                  ))}
                </div>
              </div>
            ))}
          </nav>
        </div>

        {/* Content */}
        <div className="flex-1 flex flex-col relative overflow-hidden bg-white">
          <div className="flex-1 p-8 md:p-20 overflow-y-auto">
            <AnimatePresence mode="wait">
              <div key={activeTab}>
                {renderContent()}
              </div>
            </AnimatePresence>
          </div>

          {/* Close Button UI */}
          <button 
            onClick={onClose}
            className="absolute top-8 right-8 p-3 hover:bg-gray-50 rounded-2xl text-gray-400 hover:text-black transition-all hover:scale-110 active:scale-95 z-20"
          >
            <X className="w-6 h-6 stroke-[2.5]" />
          </button>
        </div>
      </motion.div>
    </motion.div>
  );
}
