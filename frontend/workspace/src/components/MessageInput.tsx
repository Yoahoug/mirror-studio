import React, { useState, useRef, useEffect } from "react";
import { motion, AnimatePresence } from "motion/react";
import { Send, Image as ImageIcon, X, Sparkles } from "lucide-react";

interface MessageInputProps {
  onSend: (message: string, image?: string) => void;
  isLoading: boolean;
  t: any;
}

export default function MessageInput({ onSend, isLoading, t }: MessageInputProps) {
  const [input, setInput] = useState("");
  const [image, setImage] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const suggestions = t.suggestions || [
    { label: "写一段创意文案", text: "请为一家新成立的科技艺术工作室写一段富有诗意的品牌愿景文案。" },
    { label: "极简海报设计", text: "我想设计一张极简风格的海报，主题是'时间与空间'，请提供色彩建议和构图思路。" },
    { label: "代码润色", text: "我有一段 React 代码，你能帮我根据性能最佳实践进行润色和优化吗？" },
    { label: "旅行灵感", text: "我想去西藏进行一次为期 7 天的深度摄影旅行，请帮我规划一个避开人群的路线。" },
  ];

  const handleSend = () => {
    if ((input.trim() || image) && !isLoading) {
      onSend(input, image || undefined);
      setInput("");
      setImage(null);
    }
  };

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) {
      const reader = new FileReader();
      reader.onloadend = () => {
        setImage(reader.result as string);
      };
      reader.readAsDataURL(file);
    }
  };

  return (
    <div className="w-full max-w-3xl mx-auto px-4 pb-8 pt-4">
      <div className="relative group">
        <AnimatePresence>
          {!input && !image && !isLoading && (
            <motion.div
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: 10 }}
              className="flex flex-wrap gap-2 mb-4 px-2"
            >
              {suggestions.map((suggestion, i) => (
                <motion.button
                  key={i}
                  whileHover={{ y: -2, backgroundColor: "white", borderColor: "black" }}
                  whileTap={{ scale: 0.95 }}
                  onClick={() => setInput(suggestion.text)}
                  className="px-3 py-1.5 rounded-full border border-gray-100 bg-white/50 text-[11px] text-gray-500 hover:text-black transition-all shadow-sm flex items-center gap-1.5"
                >
                  <Sparkles className="w-3 h-3 text-amber-400" />
                  {suggestion.label}
                </motion.button>
              ))}
            </motion.div>
          )}
        </AnimatePresence>

        <AnimatePresence>
          {image && (
            <motion.div
              initial={{ y: 10, opacity: 0 }}
              animate={{ y: 0, opacity: 1 }}
              exit={{ y: 10, opacity: 0 }}
              className="absolute -top-20 left-0 p-2 bg-white rounded-xl shadow-lg border border-gray-100 flex items-center gap-2 transition-colors"
            >
              <img src={image} alt="Upload preview" className="w-12 h-12 rounded-lg object-cover" />
              <button 
                onClick={() => setImage(null)}
                className="p-1 hover:bg-gray-100 rounded-full text-gray-400 transition-colors"
              >
                <X className="w-4 h-4" />
              </button>
            </motion.div>
          )}
        </AnimatePresence>

        <motion.div 
          initial={false}
          animate={{ scale: isLoading ? 0.98 : 1 }}
          className="flex items-end gap-2 bg-white rounded-2xl p-2 shadow-sm border border-gray-200 focus-within:border-black focus-within:shadow-md focus-within:ring-4 focus-within:ring-black/5 transition-all duration-300 neumorph-subtle"
        >
          <input
            type="file"
            ref={fileInputRef}
            onChange={handleFileChange}
            accept="image/*"
            className="hidden"
          />
          <motion.button 
            whileHover={{ scale: 1.1 }}
            whileTap={{ scale: 0.9 }}
            onClick={() => fileInputRef.current?.click()}
            className="p-2.5 hover:bg-gray-50 rounded-xl text-gray-400 hover:text-black transition-colors"
          >
            <ImageIcon className="w-5 h-5 stroke-[1.5]" />
          </motion.button>

          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                handleSend();
              }
            }}
            placeholder={t.placeholder}
            rows={1}
            className="flex-1 py-2.5 px-2 text-sm bg-transparent outline-none resize-none min-h-[40px] max-h-48"
            style={{ height: 'auto' }}
            onInput={(e) => {
              const target = e.target as HTMLTextAreaElement;
              target.style.height = 'auto';
              target.style.height = `${target.scrollHeight}px`;
            }}
          />

          <motion.button
            whileHover={(input.trim() || image) && !isLoading ? { scale: 1.05 } : {}}
            whileTap={(input.trim() || image) && !isLoading ? { scale: 0.95 } : {}}
            onClick={handleSend}
            disabled={(!input.trim() && !image) || isLoading}
            className={`p-2.5 rounded-xl transition-all ${
              (input.trim() || image) && !isLoading
                ? "bg-black text-white shadow-lg shadow-black/10 transition-colors"
                : "bg-gray-50 text-gray-300 cursor-not-allowed transition-colors"
            }`}
          >
            <Send className="w-5 h-5 stroke-[2]" />
          </motion.button>
        </motion.div>
        
        <div className="mt-2 px-4 flex justify-start items-center transition-colors">
          <span className="text-[10px] text-gray-400 font-medium uppercase tracking-widest italic">
            {t.shortcut}
          </span>
        </div>
      </div>
    </div>
  );
}
