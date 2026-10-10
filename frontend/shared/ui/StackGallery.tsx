import { useEffect, useMemo, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { ChevronLeft, ChevronRight, Images } from "lucide-react";

/**
 * 堆叠重放画廊（工作台预览区 & 资产卡片共用）：
 * 多张图片时呈扇形堆叠，点击卡片（或左右箭头）翻到下一张，带 spring 过渡动画。
 * 单张时退化为普通展示。图片按传入顺序展示，当前张在索引 0。
 */

export interface StackGalleryImage {
  key: string | number;
  src: string;
  alt?: string;
}

interface StackGalleryProps {
  images: StackGalleryImage[];
  /** 图片宽高比（w/h），决定容器形状；不传则跟随当前图片自然尺寸。 */
  aspectRatio?: number;
  /** 点击行为：默认翻到下一张。 */
  onAdvance?: () => void;
  className?: string;
  /** 堆叠扇形的偏移幅度（px），小卡片用小值。默认 18。 */
  spread?: number;
  /** 是否显示左右箭头（预览区大图用）。默认 true；小卡片可关。 */
  showArrows?: boolean;
  /** 受控索引（预览区需要与外部状态同步时传）。 */
  index?: number;
  onIndexChange?: (index: number) => void;
}

export function StackGallery({
  images,
  aspectRatio,
  onAdvance,
  className = "",
  spread = 18,
  showArrows = true,
  index: controlledIndex,
  onIndexChange,
}: StackGalleryProps) {
  const [internalIndex, setInternalIndex] = useState(0);
  const count = images.length;
  const index = controlledIndex !== undefined ? controlledIndex : internalIndex;

  useEffect(() => {
    if (controlledIndex !== undefined) return;
    setInternalIndex((current) => (current >= count ? 0 : current));
  }, [count, controlledIndex]);

  // 展示顺序：当前张置顶，其余按原顺序跟在后面（堆叠视觉 = 最近生成在最上）。
  const ordered = useMemo(() => {
    if (count <= 1) return images;
    return Array.from({ length: count }, (_, offset) => images[(index + offset) % count]);
  }, [images, index, count]);

  const advance = () => {
    if (count <= 1) {
      onAdvance?.();
      return;
    }
    const next = (index + 1) % count;
    if (controlledIndex === undefined) setInternalIndex(next);
    onIndexChange?.(next);
    onAdvance?.();
  };

  const step = (delta: number) => {
    if (count <= 1) return;
    const next = (index + delta + count) % count;
    if (controlledIndex === undefined) setInternalIndex(next);
    onIndexChange?.(next);
  };

  if (!count) return null;

  const containerStyle = aspectRatio ? { aspectRatio: `${aspectRatio}` } : undefined;

  return (
    <div
      className={`relative select-none ${className}`}
      style={containerStyle}
      onClick={advance}
      role={count > 1 ? "button" : undefined}
      title={count > 1 ? "点击翻到下一张" : undefined}
    >
      {ordered.slice(0, Math.min(count, 3)).map((image, offset) => {
        const isTop = offset === 0;
        return (
          <motion.div
            key={image.key}
            className="absolute inset-0 overflow-hidden rounded-[1.5rem] border border-gray-100 bg-gray-50 shadow-2xl"
            initial={false}
            animate={
              count > 1
                ? {
                    x: offset * spread * 0.55,
                    y: offset * spread * 0.35,
                    rotate: offset * 2.2,
                    scale: 1 - offset * 0.035,
                    zIndex: 10 - offset,
                    opacity: offset < 2 ? 1 : 0,
                  }
                : { x: 0, y: 0, rotate: 0, scale: 1, zIndex: 10, opacity: 1 }
            }
            transition={{ type: "spring", stiffness: 320, damping: 30 }}
          >
            <img
              src={image.src}
              alt={image.alt || `生成结果 ${offset + 1}`}
              className="h-full w-full rounded-[1.5rem] object-cover"
              draggable={false}
            />
          </motion.div>
        );
      })}

      {/* 顶层图片淡入淡出：key 变化触发 crossfade，避免翻页时整卡跳动 */}
      <AnimatePresence initial={false}>
        <motion.img
          key={ordered[0].key}
          src={ordered[0].src}
          alt={ordered[0].alt || "生成结果"}
          className="absolute inset-0 h-full w-full rounded-[1.5rem] object-cover"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 0.28, ease: "easeOut" }}
          draggable={false}
        />
      </AnimatePresence>

      {count > 1 && (
        <>
          {showArrows && (
            <>
              <button
                type="button"
                onClick={(event) => {
                  event.stopPropagation();
                  step(-1);
                }}
                aria-label="上一张"
                className="absolute left-3 top-1/2 z-20 grid h-8 w-8 -translate-y-1/2 cursor-pointer place-items-center rounded-full bg-black/45 text-white backdrop-blur transition hover:bg-black/65"
              >
                <ChevronLeft className="h-4 w-4" />
              </button>
              <button
                type="button"
                onClick={(event) => {
                  event.stopPropagation();
                  step(1);
                }}
                aria-label="下一张"
                className="absolute right-3 top-1/2 z-20 grid h-8 w-8 -translate-y-1/2 cursor-pointer place-items-center rounded-full bg-black/45 text-white backdrop-blur transition hover:bg-black/65"
              >
                <ChevronRight className="h-4 w-4" />
              </button>
            </>
          )}
          <div className="absolute bottom-3 left-1/2 z-20 flex -translate-x-1/2 items-center gap-1.5 rounded-full bg-black/45 px-2.5 py-1 text-[10px] font-black text-white backdrop-blur">
            <Images className="h-3 w-3" />
            {(index + 1)} / {count}
          </div>
        </>
      )}
    </div>
  );
}
