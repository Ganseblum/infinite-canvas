"use client";

import { useState } from "react";

/**
 * Hero 背景「放映中」视频。
 * 视频加载失败（断网 / 文件未就位）时自动降级为渐变底，版式不受影响。
 * 占位视频：MDN 公共示例素材（CC0），上线前替换为自有 showreel。
 */
export default function HeroVideo({ src, poster }: { src: string; poster: string }) {
  const [failed, setFailed] = useState(false);
  return (
    <div className={`hero-media${failed ? " no-video" : ""}`} aria-hidden="true">
      <video
        src={src}
        poster={poster}
        autoPlay
        muted
        loop
        playsInline
        onError={() => setFailed(true)}
      />
      <div className="hero-veil" />
    </div>
  );
}
