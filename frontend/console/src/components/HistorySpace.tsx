import { motion } from "motion/react";
import { Search, Filter, MessageSquare, Clock, Trash2, ExternalLink } from "lucide-react";

export default function HistorySpace({ addToast, t }: { addToast?: (msg: string, type?: "success" | "error" | "info") => void, t: any }) {
  const historyItems = [
    { id: "1", title: t.items?.[0] || "品牌愿景文案内容", date: "2h ago", type: "Text", tokens: 420 },
    { id: "2", title: t.items?.[1] || "极简海报构图建议", date: "Yesterday", type: "Design", tokens: 850 },
    { id: "3", title: t.items?.[2] || "React 性能优化代码", date: "2024-04-20", type: "Code", tokens: 1200 },
    { id: "4", title: t.items?.[3] || "西藏旅行路线规划", date: "2024-04-18", type: "Chat", tokens: 600 },
    { id: "5", title: t.items?.[4] || "智能家居控制逻辑", date: "2024-04-15", type: "Code", tokens: 950 },
  ];

  const handleDelete = (e: React.MouseEvent, title: string) => {
    e.stopPropagation();
    if (addToast) addToast(`"${title}" ${t.deleteToast}`, "info");
  };

  const handleOpen = (e: React.MouseEvent, title: string) => {
    e.stopPropagation();
    if (addToast) addToast(`${t.openToast} "${title}"`, "success");
  };

  return (
    <motion.div 
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      className="flex-1 overflow-y-auto"
    >
      <div className="max-w-5xl mx-auto p-4 md:p-8 w-full space-y-8">
        <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
          <div>
            <h1 className="text-3xl font-bold tracking-tight text-gray-900">{t.title}</h1>
            <p className="text-gray-400 mt-1 font-light italic">{t.subtitle}</p>
          </div>
          <div className="flex items-center gap-3 w-full md:w-auto">
            <div className="relative flex-1 md:w-64">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" />
              <input 
                type="text" 
                placeholder={t.search} 
                className="w-full pl-10 pr-4 py-2 bg-white border border-gray-100 rounded-xl text-sm focus:outline-none focus:ring-2 focus:ring-black/5 focus:border-gray-300 transition-all shadow-sm"
              />
            </div>
            <button 
              onClick={() => addToast && addToast(t.filtering, "info")}
              className="p-2 bg-white border border-gray-100 rounded-xl shadow-sm text-gray-500 hover:text-black transition-colors"
            >
              <Filter className="w-5 h-5" />
            </button>
          </div>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-6">
        {historyItems.map((item, i) => (
          <motion.div
            key={item.id}
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: i * 0.1 }}
            whileHover={{ y: -5, boxShadow: "0 20px 25px -5px rgb(0 0 0 / 0.1)" }}
            onClick={() => addToast && addToast(`${t.restoreToast}：${item.title}`)}
            className="group bg-white p-6 rounded-3xl border border-gray-100 shadow-sm transition-all cursor-pointer relative overflow-hidden neumorph-subtle"
          >
            <div className="flex justify-between items-start mb-4">
              <div className={`p-2.5 rounded-2xl ${
                item.type === 'Text' ? 'bg-blue-50 text-blue-500' :
                item.type === 'Design' ? 'bg-purple-50 text-purple-500' :
                item.type === 'Code' ? 'bg-emerald-50 text-emerald-500' :
                'bg-amber-50 text-amber-500'
              }`}>
                <MessageSquare className="w-5 h-5" />
              </div>
              <div className="flex gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                <button 
                  onClick={(e) => handleDelete(e, item.title)}
                  className="p-1.5 hover:bg-gray-50 rounded-lg text-gray-400 hover:text-red-500 transition-colors"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
                <button 
                  onClick={(e) => handleOpen(e, item.title)}
                  className="p-1.5 hover:bg-gray-50 rounded-lg text-gray-400 hover:text-black transition-colors"
                >
                  <ExternalLink className="w-4 h-4" />
                </button>
              </div>
            </div>
            
            <h3 className="text-[17px] font-semibold text-gray-900 group-hover:text-black leading-tight mb-2">
              {item.title}
            </h3>
            
            <div className="flex items-center gap-4 mt-auto pt-4 border-t border-gray-50 uppercase tracking-tighter text-[10px] font-bold text-gray-400">
              <div className="flex items-center gap-1.2">
                <Clock className="w-3 h-3" />
                <span>{item.date}</span>
              </div>
              <div className="flex items-center gap-1.2">
                <div className="w-1 h-1 rounded-full bg-gray-200" />
                <span>{item.tokens} {t.tokens}</span>
              </div>
            </div>
          </motion.div>
        ))}
      </div>
    </div>
  </motion.div>
  );
}
