import { motion, useMotionValue, useSpring, useTransform } from "motion/react";
import { Cloud, Sparkles, Wand2, Lightbulb } from "lucide-react";
import { useEffect } from "react";

export default function WelcomeSpace({ t }: { t: any }) {
  const mouseX = useMotionValue(0);
  const mouseY = useMotionValue(0);

  const springConfig = { damping: 25, stiffness: 150 };
  const dx = useSpring(mouseX, springConfig);
  const dy = useSpring(mouseY, springConfig);

  const rotateX = useTransform(dy, [-300, 300], [5, -5]);
  const rotateY = useTransform(dx, [-300, 300], [-5, 5]);

  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      mouseX.set(e.clientX - window.innerWidth / 2);
      mouseY.set(e.clientY - window.innerHeight / 2);
    };
    window.addEventListener("mousemove", handleMouseMove);
    return () => window.removeEventListener("mousemove", handleMouseMove);
  }, [mouseX, mouseY]);

  return (
    <div className="flex-1 flex flex-col items-center justify-center p-8 text-center bg-brand-bg relative overflow-hidden perspective-1000">
      {/* Dynamic Background Elements */}
      <motion.div 
        style={{ x: dx, y: dy }}
        className="absolute top-1/4 left-1/4 w-96 h-96 bg-gray-100 rounded-full blur-3xl opacity-40 -z-10" 
      />
      
      <motion.div
        initial={{ y: 30, opacity: 0 }}
        animate={{ y: 0, opacity: 1 }}
        style={{ rotateX, rotateY }}
        transition={{ duration: 0.8, ease: "easeOut" }}
        className="space-y-8 max-w-lg z-10"
      >
        <div className="relative inline-block group">
          <motion.div 
            whileHover={{ scale: 1.05, rotate: 2 }}
            className="w-28 h-28 rounded-[2.5rem] bg-white shadow-2xl flex items-center justify-center mx-auto mb-4 neumorph-subtle border border-gray-50 cursor-pointer"
          >
            <Cloud className="w-12 h-12 text-gray-400 stroke-[1.2]" />
          </motion.div>
          
          <motion.div
            animate={{ 
              y: [0, -8, 0],
              scale: [1, 1.1, 1]
            }}
            transition={{ duration: 5, repeat: Infinity, ease: "easeInOut" }}
            className="absolute -top-3 -right-3 w-12 h-12 rounded-2xl bg-white shadow-xl flex items-center justify-center border border-gray-50"
          >
            <Sparkles className="w-6 h-6 text-yellow-400" />
          </motion.div>

          <motion.div
            initial={{ scale: 0 }}
            animate={{ scale: 1 }}
            transition={{ delay: 0.5, type: "spring" }}
            className="absolute -bottom-2 -left-4 w-10 h-10 rounded-xl bg-white shadow-lg flex items-center justify-center border border-gray-50"
          >
            <Wand2 className="w-5 h-5 text-indigo-400" />
          </motion.div>
        </div>

        <div className="space-y-3">
          <motion.h1 
            initial={{ opacity: 0, y: 10 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: 0.2 }}
            className="text-5xl font-bold tracking-tight text-gray-900"
          >
            {t.title}
          </motion.h1>
          <motion.p 
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            transition={{ delay: 0.4 }}
            className="text-gray-400 text-xl font-light leading-relaxed max-w-sm mx-auto"
          >
            {t.subtitle}
          </motion.p>
        </div>

        <motion.div 
          initial={{ opacity: 0, y: 20 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.6 }}
          className="grid grid-cols-2 gap-4 max-w-md mx-auto pt-4"
        >
          {[
            { icon: Lightbulb, text: t.inspiration, color: "text-amber-500" },
            { icon: Wand2, text: t.quickGenerate, color: "text-indigo-500" }
          ].map((item, i) => (
            <motion.div 
              key={i}
              whileHover={{ y: -5, backgroundColor: "rgba(255,255,255,1)" }}
              className="flex items-center gap-3 p-4 bg-white/50 rounded-2xl border border-gray-100 cursor-pointer transition-colors shadow-sm"
            >
              <item.icon className={`w-5 h-5 ${item.color}`} />
              <span className="text-sm font-medium text-gray-600">{item.text}</span>
            </motion.div>
          ))}
        </motion.div>

        <div className="flex items-center justify-center gap-3 pt-4">
          <span className="h-[1px] w-8 bg-gray-200" />
          <span className="text-[10px] uppercase tracking-[0.3em] text-gray-300 font-bold italic">{t.meet}</span>
          <span className="h-[1px] w-8 bg-gray-200" />
        </div>
      </motion.div>
    </div>
  );
}
