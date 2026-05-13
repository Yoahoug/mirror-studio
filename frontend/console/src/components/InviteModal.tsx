import { motion } from "motion/react";
import { 
  X, Gift, Share2, Copy, RefreshCw, 
  UserPlus, Award, History, Plus, 
  Eye, EyeOff, Key
} from "lucide-react";
import { useState } from "react";
import { translations } from "../i18n";

export default function InviteModal({ onClose, addToast, t }: { 
  onClose: () => void,
  addToast?: (msg: string, type?: "success" | "error" | "info") => void,
  t: any
}) {
  const [inviteCode] = useState("YM-34n5j-3921-resp7k");
  const [showKey, setShowKey] = useState(false);

  const copyInvite = () => {
    navigator.clipboard.writeText(inviteCode);
    if (addToast) addToast(t.copyToast, "success");
  };

  return (
    <motion.div 
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-[110] flex items-center justify-center p-4 bg-black/40 backdrop-blur-sm"
    >
      <motion.div 
        initial={{ scale: 0.9, y: 20, opacity: 0 }}
        animate={{ scale: 1, y: 0, opacity: 1 }}
        exit={{ scale: 0.9, y: 20, opacity: 0 }}
        className="bg-[#FAFAFA] w-full max-w-5xl h-[90vh] rounded-[2.5rem] shadow-2xl flex flex-col overflow-hidden"
      >
        <div className="flex-1 overflow-y-auto p-8 md:p-12 space-y-10 relative">
          {/* Header Close - Moved inside relative container */}
          <button 
            onClick={onClose}
            className="absolute top-6 right-8 p-2 hover:bg-gray-100 rounded-full text-gray-400 hover:text-black transition-all z-20"
          >
            <X className="w-6 h-6" />
          </button>

          {/* Top Banner Section */}
          <section className="bg-white rounded-[2.5rem] p-10 border border-gray-100 shadow-sm relative overflow-hidden flex flex-col md:flex-row justify-between items-center gap-8">
            <div className="space-y-4 flex-1">
              <div className="inline-flex items-center gap-2 px-3 py-1 bg-gray-50 border border-gray-100 rounded-full">
                <Gift className="w-3.5 h-3.5 text-gray-400" />
                <span className="text-[10px] font-black uppercase tracking-widest text-gray-500">{t.gift}</span>
              </div>
              <h2 className="text-4xl font-black tracking-tight text-gray-900">{t.title}</h2>
              <p className="text-gray-400 font-medium">{t.subtitle}</p>
              
              <div className="flex gap-4 pt-2">
                <div className="px-4 py-2 bg-gray-50 text-[10px] font-black text-gray-400 uppercase tracking-widest rounded-full border border-gray-100">{t.bonus}</div>
                <div className="px-4 py-2 bg-gray-50 text-[10px] font-black text-gray-400 uppercase tracking-widest rounded-full border border-gray-100">{t.record}</div>
              </div>
            </div>

            <div className="w-full md:w-56 aspect-[4/3] bg-[#FEF3C7] rounded-[2rem] p-6 flex flex-col justify-center items-center relative shadow-inner">
              <div className="absolute top-4 left-4 flex items-center gap-1 text-[#D97706]">
                <Award className="w-4 h-4" />
                <span className="text-[10px] font-black uppercase tracking-widest">{t.rewardTitle}</span>
              </div>
              <div className="text-center">
                <span className="text-6xl font-black text-[#D97706] tracking-tighter">1W</span>
                <span className="text-sm font-black text-[#D97706] ml-1 uppercase tracking-tighter">{translations.zh.sidebar.engine.includes('Engine') ? 'Credits' : '积分'}</span>
              </div>
              <p className="text-[9px] text-[#D97706]/70 mt-2 font-bold text-center">{t.rewardDesc}</p>
            </div>
          </section>

          {/* Invite Link Section */}
          <section className="bg-white rounded-[2.5rem] p-10 border border-gray-100 shadow-sm space-y-8">
            <div className="flex justify-between items-end">
              <div className="space-y-2">
                <h3 className="text-xl font-black text-gray-900">{t.linkTitle}</h3>
                <p className="text-sm text-gray-400 font-medium">{t.linkDesc}</p>
                <div className="inline-block px-3 py-1.5 bg-gray-50 border border-gray-100 rounded-full text-[10px] font-black text-gray-400 uppercase tracking-widest">
                  {t.bonusInfo}
                </div>
              </div>
              <button 
                onClick={() => addToast && addToast(t.refreshToast, "info")}
                className="flex items-center gap-2 px-4 py-2 bg-gray-50 hover:bg-gray-100 text-gray-400 hover:text-black rounded-xl transition-all"
              >
                <RefreshCw className="w-3.5 h-3.5" />
                <span className="text-xs font-bold">{t.refresh}</span>
              </button>
            </div>

            <div className="flex gap-4">
              <div className="flex-1 bg-gray-50 border border-gray-100 px-6 py-4 rounded-2xl text-sm font-medium text-gray-400 flex items-center">
                {inviteCode}
              </div>
              <button 
                onClick={copyInvite}
                className="px-8 py-4 bg-gray-500 hover:bg-black text-white rounded-2xl flex items-center gap-3 transition-all font-bold shadow-lg shadow-gray-200"
              >
                <Copy className="w-4 h-4" />
                <span>{t.copyBtn}</span>
              </button>
            </div>

            <div className="grid grid-cols-2 gap-6 pt-4">
              <div className="bg-gray-50 rounded-2xl p-6 border border-gray-50">
                <p className="text-[10px] font-black text-gray-400 uppercase tracking-widest mb-1">{t.earned}</p>
                <p className="text-3xl font-black text-gray-900">0</p>
              </div>
              <div className="bg-gray-50 rounded-2xl p-6 border border-gray-50">
                <p className="text-[10px] font-black text-gray-400 uppercase tracking-widest mb-1">{t.invited}</p>
                <p className="text-3xl font-black text-gray-900">0</p>
              </div>
            </div>
          </section>

          {/* Bottom Grid: My Keys & Success History */}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-8">
            <section className="bg-white rounded-[2.5rem] p-10 border border-gray-100 shadow-sm space-y-6 flex flex-col h-full">
              <div className="flex justify-between items-center">
                <h3 className="text-xl font-black text-gray-900 flex items-center gap-2">
                  {t.myKeys} 
                </h3>
                <button className="flex items-center gap-1.5 px-3 py-1.5 bg-white border border-gray-100 rounded-xl hover:border-black transition-all">
                  <Plus className="w-3.5 h-3.5" />
                  <span className="text-[11px] font-black uppercase tracking-widest">{t.add}</span>
                </button>
              </div>

              <div className="flex-1 bg-[#FAFAFA] border border-gray-100 rounded-[2rem] p-8 space-y-6 relative group overflow-hidden">
                <div className="flex justify-between items-start">
                  <div className="px-3 py-1 bg-[#FFFBEB] text-[#D97706] text-[9px] font-black uppercase tracking-widest rounded-full border border-[#FEF3C7]">
                    {t.currentKey}
                  </div>
                  <div className="flex items-center gap-4">
                    <div className="flex items-center gap-1 text-[10px] font-bold text-gray-300">
                      <RefreshCw className="w-2.5 h-2.5" />
                      {t.balance} 800
                    </div>
                    <div className="text-[10px] font-black text-gray-300 tracking-widest uppercase">{t.using}</div>
                  </div>
                </div>

                <div className="py-4 border-b border-gray-50">
                  <p className="font-mono text-lg font-bold text-gray-700 tracking-wider">
                    {showKey ? inviteCode : inviteCode.replace(/(.{4}).*(.{6})/, "$1****************$2")}
                  </p>
                </div>

                <div className="flex gap-4 pt-2">
                  <button 
                    onClick={() => setShowKey(!showKey)}
                    className="flex items-center gap-2 text-[10px] font-black uppercase tracking-widest text-gray-400 hover:text-black transition-all"
                  >
                    {showKey ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
                    <span>{showKey ? t.hide : t.view}</span>
                  </button>
                  <button 
                    onClick={() => {
                        navigator.clipboard.writeText(inviteCode);
                        addToast && addToast(t.keyCopyToast);
                    }}
                    className="flex items-center gap-2 text-[10px] font-black uppercase tracking-widest text-gray-400 hover:text-black transition-all"
                  >
                    <Copy className="w-3.5 h-3.5" />
                    <span>{t.copy}</span>
                  </button>
                </div>
              </div>
            </section>

            <section className="bg-white rounded-[2.5rem] p-10 border border-gray-100 shadow-sm flex flex-col h-full">
              <h3 className="text-xl font-black text-gray-900 mb-6">{t.recordsTitle}</h3>
              <div className="flex-1 flex flex-col items-center justify-center text-center space-y-4 opacity-40">
                <div className="w-16 h-16 bg-gray-50 rounded-full flex items-center justify-center">
                  <UserPlus className="w-8 h-8 text-gray-300" />
                </div>
                <p className="text-sm text-gray-400 font-medium">{t.noRecords}</p>
              </div>
            </section>
          </div>
        </div>
      </motion.div>
    </motion.div>
  );
}
