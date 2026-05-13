import { motion } from "motion/react";
import { X, CreditCard, ShieldCheck, Zap, Star } from "lucide-react";

export default function RechargeModal({ onClose, t }: { onClose: () => void, t: any }) {
  const plans = [
    { title: t.plans[0].title, originalPrice: "49", price: "29", tokens: "5,000", features: t.plans[0].features },
    { title: t.plans[1].title, originalPrice: "199", price: "99", tokens: "20,000", features: t.plans[1].features, recommended: true },
    { title: t.plans[2].title, originalPrice: "499", price: "299", tokens: "100,000", features: t.plans[2].features },
  ];

  return (
    <motion.div 
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-[100] flex items-center justify-center p-4 bg-black/40 backdrop-blur-sm"
    >
      <motion.div 
        initial={{ scale: 0.95, opacity: 0, y: 20 }}
        animate={{ scale: 1, opacity: 1, y: 0 }}
        exit={{ scale: 0.95, opacity: 0, y: 20 }}
        className="bg-white w-full max-w-4xl max-h-[90vh] overflow-y-auto rounded-[2rem] md:rounded-[3rem] shadow-2xl relative p-6 md:p-10 neumorph-subtle"
      >
        <button 
          onClick={onClose}
          className="absolute top-4 right-4 md:top-8 md:right-8 p-2 hover:bg-gray-50 rounded-full text-gray-400 hover:text-black transition-colors z-10"
        >
          <X className="w-5 h-5 md:w-6 md:h-6" />
        </button>

        <div className="text-center mb-8 md:mb-12 space-y-2">
          <div className="inline-flex items-center gap-2 px-3 py-1 bg-amber-50 rounded-full mb-2">
            <Zap className="w-4 h-4 text-amber-500 fill-amber-500" />
            <span className="text-xs font-bold text-amber-600 uppercase tracking-widest">{t.energy}</span>
          </div>
          <h2 className="text-4xl font-bold tracking-tight">{t.title}</h2>
          <p className="text-gray-400 font-light italic">{t.subtitle}</p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
          {plans.map((plan, i) => (
            <motion.div
              key={i}
              whileHover={{ y: -8 }}
              className={`relative p-8 rounded-[2.5rem] border ${
                plan.recommended ? 'border-black shadow-xl' : 'border-gray-100'
              } bg-white transition-all`}
            >
              {plan.recommended && (
                <div className="absolute -top-4 left-1/2 -translate-x-1/2 bg-black text-white text-[10px] uppercase font-bold py-1.5 px-4 rounded-full tracking-[0.2em] shadow-lg">
                  {t.recommended}
                </div>
              )}
              
              <div className="mb-6">
                <h3 className="text-lg font-bold mb-1">{plan.title}</h3>
                <div className="flex flex-col">
                  <span className="text-[10px] font-bold text-gray-300 line-through">{t.originalPrice} ¥{plan.originalPrice}</span>
                  <div className="flex items-baseline gap-1">
                    <span className="text-3xl font-black">¥{plan.price}</span>
                    <span className="text-gray-400 text-sm font-light italic">{t.priceMonth}</span>
                  </div>
                </div>
              </div>

              <div className="space-y-4 mb-8">
                <div className="flex items-center gap-2 text-sm font-medium text-gray-700">
                  <Star className="w-4 h-4 text-amber-400 fill-amber-400" />
                  <span>{t.includeTokens} {plan.tokens} {t.tokens}</span>
                </div>
                {plan.features.map((f, fi) => (
                  <div key={fi} className="flex items-center gap-2 text-xs text-gray-400">
                    <ShieldCheck className="w-3.5 h-3.5 text-gray-300" />
                    <span>{f}</span>
                  </div>
                ))}
              </div>

              <button className={`w-full py-3.5 rounded-2xl font-bold text-sm transition-all ${
                plan.recommended 
                  ? 'bg-black text-white shadow-lg shadow-black/20 hover:scale-[1.02]' 
                  : 'bg-gray-50 text-gray-400 hover:bg-gray-100 hover:text-black'
              }`}>
                {t.subscribe}
              </button>
            </motion.div>
          ))}
        </div>

        <div className="mt-12 pt-8 border-t border-gray-50 flex items-center justify-between text-[10px] font-bold text-gray-300 uppercase tracking-widest italic">
          <div className="flex items-center gap-2">
            <CreditCard className="w-4 h-4" />
            <span>{t.paymentSupport}</span>
          </div>
          <span>{t.secure}</span>
        </div>
      </motion.div>
    </motion.div>

  );
}
