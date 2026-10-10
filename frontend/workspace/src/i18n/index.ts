export const translations = {
  zh: {
    sidebar: {
      create: "开始创作",
      comfy: "Comfy 生图",
      tts: "TTS 语音合成",
      novelviz: "小说可视化",
      newChat: "新生图",
      history: "历史记录",
      about: "关于绘镜",
      engine: "绘镜 Creative Engine",
      tagline: "© 2026 Yoahoug. All Rights Reserved.",
      // 版本号来自仓库根 VERSION（Vite 注入，见 shared/app-version.mjs）
      version: `v${import.meta.env.VITE_APP_VERSION || "0.0.0"}`,
      stable: "2026 Stable"
    },
    header: {
      creationCore: "生图工作台 (Image Studio)",
      comfy: "Comfy 生图 (ComfyUI Studio)",
      tts: "TTS 语音合成 (TTS Studio)",
      novelviz: "小说场景可视化 (Novel Cinema)",
      history: "历史记录 (History)",
      brand: "绘镜",
      modelSwitch: "模型切换"
    },
    history: {
      title: "历史记录",
      subtitle: "回顾您的创意映射旅程 (Mirror of past inspirations)",
      search: "搜索历史内容...",
      tokens: "令牌",
      deleteToast: "已从历史记录中移除",
      openToast: "正在载入创意项目",
      restoreToast: "正在恢复项目",
      filtering: "正在筛选结果..."
    },
    toolbar: {
      remaining: "剩余",
      frozen: "冻结",
      quickRecharge: "快速充值"
    },
    welcome: {
      title: "新创作，新起点",
      subtitle: "探索无限可能，将您的想法转化为现实。",
      inspiration: "获取灵感",
      quickGenerate: "快速生成",
      meet: "Nice to meet you"
    },
    input: {
      placeholder: "输入您的灵感或需求...",
      send: "发送",
      upload: "上传图片",
      shortcut: "Shift + Enter 换行",
      suggestions: [
        { label: "写一段创意文案", text: "请为一家新成立的科技艺术工作室写一段富有诗意的品牌愿景文案。" },
        { label: "极简海报设计", text: "我想设计一张极简风格的海报，主题是'时间与空间'，请提供色彩建议和构图思路。" },
        { label: "代码润色", text: "我有一段 React 代码，你能帮我根据性能最佳实践进行润色和优化吗？" },
        { label: "旅行灵感", text: "我想去西藏进行一次为期 7 天的深度摄影旅行，请帮我规划一个避开人群的路线。" },
      ]
    },
    userPanel: {
      creator: "镜界创造者 (Mirror Creator)",
      invite: "邀请有礼",
      quota: "当前配额",
      privacy: "隐私安全",
      support: "支持反馈",
      language: "多语言切换",
      logout: "注销登录",
      invitationBonus: "+1W 积分"
    },
    recharge: {
      energy: "能量补给",
      title: "为您的创意加成",
      subtitle: "选择最适合您的方案，通过绘镜映射更多精彩 (Refuel your inspiration mirror)",
      recommended: "最受推荐",
      priceMonth: "/月",
      originalPrice: "原价",
      includeTokens: "包含",
      tokens: "令牌",
      subscribe: "立即订阅",
      paymentSupport: "支持微信 / 支付宝 / Visa",
      secure: "绘镜安全支付保障",
      plans: [
        { title: "基础版", features: ["优先响应", "高清图片生成", "标准对话历史"] },
        { title: "专业版", features: ["极速响应", "4K 超清生成", "无限对话历史", "早鸟功能体验"] },
        { title: "团队版", features: ["API 访问权限", "专属客户经理", "多端同步", "定制化模型微调"] },
      ]
    },
    invite: {
      gift: "邀请有礼",
      title: "喊朋友，拿福利！邀请有礼啦",
      subtitle: "脑洞再大，云梦来画！云友计划启动，一起来发出这封邀请函吧",
      bonus: "双方奖励实时到账",
      record: "绑定后自动记录",
      rewardTitle: "本次邀请奖励",
      rewardBonus: "1W 积分",
      rewardDesc: "每成功邀请1位好友，双方各得+10,000积分密钥。",
      linkTitle: "你的专属邀请链接",
      linkDesc: "分享链接，好友购买新手礼包及以上套餐，完成绑定后，双方奖励实时到账。",
      bonusInfo: "每成功 1 位好友，双方各得 +10,000 积分",
      refresh: "刷新",
      copyBtn: "立即复制去邀请",
      earned: "我已到账奖励",
      invited: "成功邀请人数",
      myKeys: "我的密钥",
      add: "添加",
      currentKey: "当前使用密钥",
      balance: "余额",
      using: "当前使用",
      hide: "隐藏",
      view: "查看",
      copy: "复制",
      recordsTitle: "成功邀请记录",
      noRecords: "暂无邀请记录，去分享链接吧。",
      copyToast: "邀请链接已复制，快去分享给好友吧！",
      keyCopyToast: "密钥已复制",
      refreshToast: "正在刷新..."
    },
    settings: {
      title: "系统设置",
      subtitle: "ArtMirror Config",
      tabs: {
        account: "账户设置",
        pref: "应用偏好",
        notify: "通知与隐私"
      },
      labels: {
        profile: "个人资料",
        security: "安全中心",
        billing: "财务记录",
        ui: "界面表现",
        language: "语言与区域",
        ai: "AI 模型配置",
        notif_push: "推送管理",
        privacy: "隐私协议",
        data: "数据管理"
      },
      save: "保持更改 (Save Changes)"
    },
    toasts: {
      newChat: "已回到生图工作台",
      copySuccess: "内容已复制到剪贴板",
      modelSwitched: "已切换至",
      fail: "操作失败，请重试"
    }
  },
  en: {
    sidebar: {
      create: "Start Creation",
      comfy: "Comfy Studio",
      tts: "TTS Studio",
      novelviz: "Novel Cinema",
      newChat: "New Image",
      history: "History",
      about: "About Mirror Lens",
      engine: "Mirror Lens Creative Engine",
      tagline: "© 2026 Yoahoug. All Rights Reserved.",
      // 版本号来自仓库根 VERSION（Vite 注入，见 shared/app-version.mjs）
      version: `v${import.meta.env.VITE_APP_VERSION || "0.0.0"}`,
      stable: "2026 Stable"
    },
    header: {
      creationCore: "Image Studio",
      comfy: "Comfy Studio (ComfyUI)",
      tts: "TTS Studio (Voice)",
      novelviz: "Novel Cinema",
      history: "History",
      brand: "Mirror Lens",
      modelSwitch: "Model Switch"
    },
    history: {
      title: "History",
      subtitle: "Mirror of past inspirations",
      search: "Search history...",
      tokens: "Tokens",
      deleteToast: "Removed from history",
      openToast: "Loading creative project",
      restoreToast: "Restoring project",
      filtering: "Filtering results..."
    },
    toolbar: {
      remaining: "Remains",
      frozen: "Frozen",
      quickRecharge: "Quick Top-up"
    },
    welcome: {
      title: "New Creation, New Start",
      subtitle: "Explore infinite possibilities and turn your ideas into reality.",
      inspiration: "Get Inspiration",
      quickGenerate: "Quick Generate",
      meet: "Nice to meet you"
    },
    input: {
      placeholder: "Enter your inspiration or requirement...",
      send: "Send",
      upload: "Upload Image",
      shortcut: "Shift + Enter to wrap",
      suggestions: [
        { label: "Creative Copywriting", text: "Please write a poetic brand vision copy for a newly established tech art studio." },
        { label: "Minimalist Poster", text: "I want to design a minimalist poster with the theme 'Time and Space', please provide color suggestions and composition ideas." },
        { label: "Code Polishing", text: "I have a piece of React code, can you help me polish and optimize it according to performance best practices?" },
        { label: "Travel Inspiration", text: "I want to take a 7-day deep photography trip to Tibet, please help me plan a route that avoids crowds." },
      ]
    },
    userPanel: {
      creator: "Mirror Creator",
      invite: "Invite & Earn",
      quota: "Current Quota",
      privacy: "Privacy",
      support: "Support",
      language: "Language Switch",
      logout: "Log Out",
      invitationBonus: "+10K Credits"
    },
    recharge: {
      energy: "Energy Refuel",
      title: "Boost Your Creativity",
      subtitle: "Choose the plan that suits you best and refuel your inspiration mirror.",
      recommended: "Recommended",
      priceMonth: "/mo",
      originalPrice: "Original",
      includeTokens: "Includes",
      tokens: "Tokens",
      subscribe: "Subscribe Now",
      paymentSupport: "Supports WeChat / Alipay / Visa",
      secure: "Mirror Lens Secure Payment",
      plans: [
        { title: "Basic", features: ["Priority Response", "HD Image Generation", "Standard History"] },
        { title: "Pro", features: ["Instant Response", "4K Ultra HD", "Infinite History", "Early Access"] },
        { title: "Team", features: ["API Access", "Dedicated Manager", "Multi-sync", "Model Fine-tuning"] },
      ]
    },
    invite: {
      gift: "Invite & Earn",
      title: "Invite Friends, Get Rewards!",
      subtitle: "Unleash your imagination! Join the Dreamer Program and share this invitation.",
      bonus: "Instant Arrival",
      record: "Automatic Recording",
      rewardTitle: "Invite Reward",
      rewardBonus: "10K Credits",
      rewardDesc: "For every successful invite, both parties get +10,000 credit keys.",
      linkTitle: "Your Exclusive Invite Link",
      linkDesc: "Share the link. When friends purchase a package, rewards are settled instantly.",
      bonusInfo: "Each friend earns you both +10,000 credits",
      refresh: "Refresh",
      copyBtn: "Copy Link & Invite",
      earned: "Rewards Earned",
      invited: "Friends Invited",
      myKeys: "My Keys",
      add: "Add",
      currentKey: "Active Key",
      balance: "Balance",
      using: "Current",
      hide: "Hide",
      view: "View",
      copy: "Copy",
      recordsTitle: "Invite History",
      noRecords: "No records yet. Share your link to start!",
      copyToast: "Invite link copied! Go share it with your friends.",
      keyCopyToast: "Key copied to clipboard",
      refreshToast: "Refreshing..."
    },
    settings: {
      title: "Settings",
      subtitle: "ArtMirror Config",
      tabs: {
        account: "Account",
        pref: "Preferences",
        notify: "Privacy"
      },
      labels: {
        profile: "Profile",
        security: "Security",
        billing: "Billing",
        ui: "Interface",
        language: "Language",
        ai: "AI Model",
        notif_push: "Notifications",
        privacy: "Privacy Policy",
        data: "Data Management"
      },
      save: "Save Changes"
    },
    toasts: {
      newChat: "Image studio is ready",
      copySuccess: "Content copied to clipboard",
      modelSwitched: "Switched to",
      fail: "Operation failed, please try again"
    }
  }
};

export type Language = "zh" | "en";
