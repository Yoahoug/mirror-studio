import { useState } from "react";
import { motion, AnimatePresence } from "motion/react";
import { Bell, Check, Info, Sparkles, MessageSquare, ChevronLeft, ArrowRight } from "lucide-react";

export default function NotificationPanel({ onClose }: { onClose: () => void }) {
  const [selectedId, setSelectedId] = useState<number | null>(null);

  const notifications = [
    { id: 1, title: "系统公告", desc: "绘镜 2.0 模型版本现已上线，生成的图片更具真实感。", time: "10分钟前", type: "system", icon: Sparkles, color: "text-amber-500 bg-amber-50", content: "我们自豪地宣布，绘镜 2.0 正式发布！本次更新引入了全新的扩散算法，显著提升了光影细节和材质真实感。欢迎各位创作者尝试。" },
    { id: 2, title: "次数已刷新", desc: "今日免费次数已自动恢复，可继续提交新的生图任务。", time: "1小时前", type: "success", icon: Check, color: "text-emerald-500 bg-emerald-50", content: "系统已为你刷新今日免费次数。如需更多次数，可联系管理员在后台直接调整。" },
    { id: 3, title: "新消息", desc: "您有一条来自创作助手的回复等待查阅。", time: "昨天", type: "chat", icon: MessageSquare, color: "text-blue-500 bg-blue-50", content: "关于您上次提交的关于'超现实主义风格'的问题，我们已经准备好了深度解析报告，请登入后台查看。" },
    { id: 4, title: "安全提醒", desc: "您的账户于今日在北京 IP 登录，如非本人请尽快修改密码。", time: "2天前", type: "info", icon: Info, color: "text-gray-400 bg-gray-50", content: "系统检测到您的账户于 2024-04-23 15:30 在新设备登录。登录地点：中国 北京。如果这不是您的操作，请立即重置密码。" },
  ];

  const selectedNotification = notifications.find(n => n.id === selectedId);

  return (
    <motion.div 
      initial={{ opacity: 0, y: 10, scale: 0.95 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, y: 10, scale: 0.95 }}
      className="fixed top-[4.5rem] right-12 z-50 flex max-h-[calc(100vh-6.5rem)] w-96 flex-col overflow-hidden rounded-[2rem] border border-gray-100 bg-white shadow-2xl neumorph-subtle"
      onClick={(e) => e.stopPropagation()}
    >
      <AnimatePresence mode="wait">
        {!selectedId ? (
          <motion.div 
            key="list"
            initial={{ x: -20, opacity: 0 }}
            animate={{ x: 0, opacity: 1 }}
            exit={{ x: -20, opacity: 0 }}
            className="flex min-h-0 flex-1 flex-col"
          >
            <div className="p-6 border-b border-gray-50 flex justify-between items-center bg-gray-50/30">
              <div>
                <h3 className="font-bold text-gray-900 leading-none">通知中心</h3>
                <p className="text-[10px] text-gray-400 mt-1 uppercase tracking-widest italic font-bold">Notifications</p>
              </div>
              <button className="text-xs font-bold text-gray-300 hover:text-black transition-colors uppercase tracking-tighter">全部标记已读</button>
            </div>

            <div className="flex-1 overflow-y-auto overscroll-contain">
              {notifications.map((n, i) => (
                <div
                  key={n.id}
                  onClick={() => setSelectedId(n.id)}
                  className="p-5 border-b border-gray-50 hover:bg-gray-50 transition-colors cursor-pointer group"
                >
                  <div className="flex gap-4">
                    <div className={`w-10 h-10 rounded-2xl flex items-center justify-center shrink-0 ${n.color}`}>
                      <n.icon className="w-5 h-5 stroke-[1.8]" />
                    </div>
                    <div className="flex-1 space-y-1">
                      <div className="flex justify-between items-center">
                        <span className="text-sm font-bold text-gray-900">{n.title}</span>
                        <span className="text-[10px] font-bold text-gray-300 italic">{n.time}</span>
                      </div>
                      <p className="text-xs text-gray-500 leading-relaxed line-clamp-2">{n.desc}</p>
                    </div>
                  </div>
                </div>
              ))}
            </div>

            <div className="p-4 text-center bg-gray-50/30">
              <button className="text-xs font-bold text-gray-400 hover:text-black transition-all group flex items-center justify-center gap-1 mx-auto">
                <span>查看所有通知</span>
                <div className="w-1.5 h-1.5 rounded-full bg-gray-200 group-hover:bg-black transition-colors" />
              </button>
            </div>
          </motion.div>
        ) : (
          <motion.div 
            key="preview"
            initial={{ x: 20, opacity: 0 }}
            animate={{ x: 0, opacity: 1 }}
            exit={{ x: 20, opacity: 0 }}
            className="flex min-h-0 flex-1 flex-col overflow-y-auto p-8 overscroll-contain"
          >
            <button 
              onClick={() => setSelectedId(null)}
              className="flex items-center gap-2 text-xs font-bold text-gray-400 hover:text-black transition-colors group"
            >
              <ChevronLeft className="w-4 h-4 group-hover:-translate-x-1 transition-transform" />
              返回列表
            </button>

            <div className="mt-8 space-y-6">
              <div className="flex items-center gap-4">
                <div className={`w-14 h-14 rounded-[1.5rem] flex items-center justify-center ${selectedNotification?.color}`}>
                  {selectedNotification && <selectedNotification.icon className="w-7 h-7" />}
                </div>
                <div className="space-y-1">
                  <h4 className="text-xl font-bold text-gray-900">{selectedNotification?.title}</h4>
                  <p className="text-xs text-gray-400 font-bold">{selectedNotification?.time} · 系统精选</p>
                </div>
              </div>

              <div className="p-6 bg-gray-50 rounded-[2rem] border border-gray-100">
                <p className="text-sm text-gray-600 leading-relaxed italic">
                  "{selectedNotification?.content}"
                </p>
              </div>

              <button className="w-full py-4 bg-black text-white rounded-2xl text-xs font-bold flex items-center justify-center gap-2 hover:scale-[1.02] active:scale-[0.98] transition-all shadow-xl shadow-black/10">
                立即处理 (Action Now)
                <ArrowRight className="w-4 h-4" />
              </button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </motion.div>
  );
}
