import { motion } from "motion/react";
import { User, LogOut, ShieldCheck, Mail, Globe, Sparkles, Gift } from "lucide-react";
import { Language } from "../i18n";

export default function UserPanel({ 
  onClose, 
  onOpenInvite, 
  language, 
  onLanguageChange,
  t 
}: { 
  onClose: () => void, 
  onOpenInvite: () => void,
  language: Language,
  onLanguageChange: (lang: Language) => void,
  t: any
}) {
  return (
    <motion.div 
      initial={{ opacity: 0, y: 10, scale: 0.95 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, y: 10, scale: 0.95 }}
      className="absolute top-16 right-0 w-80 bg-white rounded-[2rem] shadow-2xl border border-gray-100 overflow-hidden z-50 neumorph-subtle mr-8"
    >
      <div className="p-8 pb-6 text-center space-y-4">
        <div className="relative inline-block mx-auto">
          <div className="w-24 h-24 rounded-[2.5rem] bg-gradient-to-tr from-gray-50 to-gray-200 border-2 border-white shadow-xl flex items-center justify-center overflow-hidden">
            <img src="https://api.dicebear.com/7.x/avataaars/svg?seed=admin" alt="Admin" className="w-full h-full" />
          </div>
          <div className="absolute -bottom-1 -right-1 w-7 h-7 bg-black rounded-xl flex items-center justify-center shadow-lg">
            <Sparkles className="w-3.5 h-3.5 text-white" />
          </div>
        </div>

        <div>
          <h3 className="text-xl font-bold tracking-tight text-gray-900 leading-none">admin</h3>
          <div className="flex items-center justify-center gap-1.5 mt-2">
            <div className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
            <span className="text-[10px] font-bold text-gray-400 uppercase tracking-widest italic leading-none">{t.creator}</span>
          </div>
        </div>
      </div>

      <div className="px-3 pb-3 space-y-1">
        <button 
          onClick={onOpenInvite}
          className="w-[calc(100%-1.5rem)] flex items-center justify-between px-6 py-4 bg-amber-50 hover:bg-amber-100 text-amber-700 transition-all rounded-2xl group mx-3 mb-2"
        >
          <div className="flex items-center gap-3">
            <Gift className="w-4 h-4 text-amber-500 transition-transform group-hover:scale-110" />
            <span className="font-bold text-sm">{t.invite}</span>
          </div>
          <span className="text-[10px] font-black uppercase tracking-widest opacity-60">{t.invitationBonus}</span>
        </button>

        <div className="p-4 bg-gray-50/50 rounded-2xl border border-gray-100 mx-3 mb-4 space-y-3">
          <div className="flex items-center justify-between text-[10px] font-black text-gray-400 uppercase tracking-widest italic">
            <span>{t.quota}</span>
            <span className="text-black">32.4k / 50k</span>
          </div>
          <div className="h-1.5 w-full bg-gray-100 rounded-full overflow-hidden shadow-inner">
            <motion.div 
              initial={{ width: 0 }}
              animate={{ width: "65%" }}
              className="h-full bg-black rounded-full" 
            />
          </div>
        </div>

        {[
          { icon: ShieldCheck, label: t.privacy, color: "text-gray-400" },
          { icon: Mail, label: t.support, color: "text-gray-400" },
        ].map((item, i) => (
          <button key={i} className="w-full flex items-center gap-3 px-6 py-3 text-sm text-gray-500 hover:text-black hover:bg-gray-50 transition-all rounded-xl group mx-3">
            <item.icon className={`w-4 h-4 transition-transform group-hover:scale-110 ${item.color}`} />
            <span className="font-medium">{item.label}</span>
          </button>
        ))}

        <button 
          onClick={() => onLanguageChange(language === 'zh' ? 'en' : 'zh')}
          className="w-full flex items-center justify-between px-6 py-3 text-sm text-gray-500 hover:text-black hover:bg-gray-50 transition-all rounded-xl group mx-3"
        >
          <div className="flex items-center gap-3">
            <Globe className="w-4 h-4 text-gray-400 transition-transform group-hover:scale-110" />
            <span className="font-medium">{t.language}</span>
          </div>
          <span className="text-[10px] font-black uppercase tracking-widest text-black/40">{language === 'zh' ? 'EN' : '中文'}</span>
        </button>

        <div className="h-[1px] bg-gray-50 mx-6 my-2" />
        
        <button className="w-full flex items-center gap-3 px-6 py-3 text-sm text-red-400 hover:text-red-500 hover:bg-red-50 transition-all rounded-xl group mx-3">
          <LogOut className="w-4 h-4 transition-transform group-hover:-translate-x-1" />
          <span className="font-bold">{t.logout}</span>
        </button>
      </div>
    </motion.div>
  );
}
