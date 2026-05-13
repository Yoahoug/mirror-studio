import { motion } from "motion/react";
import { X } from "lucide-react";

export default function RechargeModal({ onClose }: { onClose: () => void }) {
  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-[100] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm"
    >
      <motion.div
        initial={{ scale: 0.96, opacity: 0 }}
        animate={{ scale: 1, opacity: 1 }}
        exit={{ scale: 0.96, opacity: 0 }}
        className="relative w-full max-w-lg rounded-[2rem] bg-white p-8 shadow-2xl"
      >
        <button
          onClick={onClose}
          className="absolute right-4 top-4 rounded-full p-2 text-gray-400 transition-colors hover:bg-gray-50 hover:text-black"
        >
          <X className="h-5 w-5" />
        </button>
        <h2 className="text-2xl font-black tracking-tight text-gray-950">当前版本无需充值</h2>
        <p className="mt-3 text-sm leading-6 text-gray-500">
          这是绘镜轻量部署包。每个账号每天默认可免费生图，管理员可以在后台调整默认次数和单个账号剩余次数。
        </p>
      </motion.div>
    </motion.div>
  );
}
