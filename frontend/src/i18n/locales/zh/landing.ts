export default {
  guestTrial: {
    pageTitle: '免费试用',
    eyebrow: '网页版 AI',
    continueTrial: '继续试用',
    backHome: '返回主页',
    noSignup: '免注册',
    title: '不用注册，直接开聊',
    subtitle: '输入问题就能和 AI 对话。试用版仅支持文字聊天，注册后解锁生图、改图、上传文件和全部模型。',
    login: '登录',
    register: '免费注册',
    registerFree: '免费注册，解锁全部能力',
    openWorkspace: '打开网页工作台',
    model: '模型',
    remaining: '今日还可试用 {remaining} / {total} 条',
    emptyHint: '想问什么都可以，比如：',
    suggestion1: '帮我写一封礼貌的请假邮件',
    suggestion2: '用大白话解释一下什么是 AI 网关',
    suggestion3: '给我一个一周健身计划',
    placeholder: '输入你的问题，按 Enter 发送，Shift + Enter 换行',
    textOnly: '试用版仅支持文字聊天',
    send: '发送',
    stop: '停止',
    emptyAnswer: '（这次没有返回内容，换个说法再试试）',
    sendFailed: '发送失败，请稍后再试',
    captchaFirst: '请先完成上方的人机验证',
    captchaFailed: '人机验证没有通过，请再试一次',
    exhaustedTitle: '今天的试用次数用完了',
    exhaustedHint: '注册一个免费账号，就能继续聊天，还能生图、改图和上传文件。',
    disabledTitle: '网页版试用暂未开放',
    disabledHint: '注册或登录后即可使用完整的网页工作台。',
    unlockTitle: '注册后还能做这些',
    unlock1: '生图、改图：上传图片直接修改',
    unlock2: '上传文件，让 AI 帮你读文档',
    unlock3: '全部模型随时切换',
    unlock4: '接入 Codex 和各类插件'
  },
  batchImageGuide: {
    title: '图片批量生成',
    description: '一次提交多条提示词，任务完成后可统一下载图片结果'
  },
  // Home Page
  homeIntro: {
    eyebrow: 'AI 网关 · 网页版 AI',
    eyebrowGatewayOnly: 'AI 网关 · 多模型接入',
    title: '多种模型，',
    titleAccent: '都在 Codex 里用。',
    description: '共飞是一款 AI 网关：GPT、Claude、Gemini 等模型通过同一个接口地址接入 Codex，也能用在 Claude Code、Cursor、Cline、Cherry Studio 等插件里。',
    descriptionWeb: '不想装软件？直接在网页上聊天、生图、改图。',
    ctaStart: '登录 / 注册',
    ctaStartWork: '开始工作',
    ctaConsole: '进入控制台',
    ctaTrial: '免费试用',
    ctaTrialBadge: '免注册',
    trialHint: '不用注册，打开就能直接聊天。',
    ctaWeb: '打开网页工作台',
    ctaClient: '下载客户端',
    ctaClientPrimary: '下载共飞客户端',
    beginnerNote: '一个账号、一个密钥、一份余额，所有用法通用。',
    ideaNote: 'GPT、Claude、Gemini，一个密钥全接上。',
    purpose: '共飞负责连接模型、管理账号和费用；你在 Codex、各类插件或网页工作台里说出需求。',
    purposeNoWeb: '共飞负责连接模型、管理账号和费用；你在 Codex 或各类插件里说出需求。',
    map: {
      title: '它是怎么接起来的',
      hint: '一个接口地址，把各家模型接到你常用的地方',
      models: '模型',
      more: '以及更多模型',
      gateway: '{name} 网关',
      protocols: '兼容 OpenAI · Anthropic · Gemini 接口',
      targets: '用在哪里',
      codexHint: 'CLI · 桌面版',
      webHint: '浏览器直接用',
      fact1: '一个接口地址',
      fact2: '一个密钥，所有模型通用',
      fact3: '一份余额，按量计费'
    },
    ways: {
      eyebrow: '怎么用',
      title: '选一种\n顺手的方式',
      note: '几种方式共用同一个账号和余额，可以同时用。',
      api: {
        title: '接入 Codex 和各类插件',
        p1: '在 Codex CLI、Codex 桌面版里切换 GPT、Claude 等模型。',
        p2: 'Claude Code、Cursor、Cline、Cherry Studio 等兼容 OpenAI、Anthropic、Gemini 接口的工具都能接。',
        action: '创建密钥'
      },
      web: {
        title: '网页版 AI',
        p1: '打开浏览器就能用，不用装任何软件。',
        p2: '聊天、生图、上传图片直接修改，还有无限画布和图库。',
        action: '打开网页工作台'
      },
      client: {
        title: '共飞助手客户端',
        badge: '推荐',
        p1: '下载页同时提供 Codex 和共飞客户端，装好就能用，不用手动改任何配置。',
        p2: '账号、分组、余额在客户端里一目了然；还能帮你节省 Token，用量和节省比例随时看得见。',
        action: '下载客户端',
        guide: '看安装教程'
      }
    },
    codex: {
      eyebrow: '在 Codex 里使用',
      title: '三步，把共飞接进 Codex。',
      step1Title: '创建一个密钥',
      step1: '登录后在「API 密钥」里新建，一个密钥所有模型通用。',
      step2Title: '写进 Codex 配置',
      step2: '把下面这段写进 config.toml；密钥页的「使用密钥」里有完整版本，可以一键复制。',
      step3Title: '打开 Codex，开始干活',
      step3: '在模型列表里切换 GPT、Claude 等模型，说出你想做的事。',
      comment: '接口地址已按本站自动填好',
      action: '去创建密钥',
      clientTitle: '三步，开始在 Codex 里干活。',
      clientStep1Title: '下载两个软件',
      clientStep1: '下载页同时提供共飞客户端和 Codex，按你的电脑选对应版本。',
      clientStep2Title: '登录共飞客户端',
      clientStep2: '登录后客户端自动接好 Codex，不用改任何配置文件。',
      clientStep3Title: '打开 Codex，开始干活',
      clientStep3: '说出你想做的事；客户端帮你节省 Token，用量随时可查。',
      clientAction: '下载共飞客户端',
      guide: '看安装教程',
      manualToggle: '习惯手动配置？查看 config.toml 写法'
    }
  },
  home: {
    viewOnGithub: '在 GitHub 上查看',
    viewDocs: '查看文档',
    docs: '文档',
    switchToLight: '切换到浅色模式',
    switchToDark: '切换到深色模式',
    dashboard: '控制台',
    login: '登录',
    getStarted: '立即开始',
    register: '注册',
    goToDashboard: '进入控制台',
    downloadClient: '下载客户端',
    loginExisting: '已有账号，直接登录',
    // 新增：面向用户的价值主张
    heroSubtitle: '一个客户端，用上 Claude、GPT、Gemini',
    heroDescription: '开箱即用，注册登录、分组切换、余额充值全部在客户端内完成',
    tags: {
      allInOne: '客户端内完成注册充值',
      groupSwitch: '分组稳定切换',
      payAsYouGo: '按量计费'
    },
    // 首页三张卖点卡片：压缩省钱、价格倍率、线路稳定性，替换掉原来信息量很小的三个胶囊标签
    benefits: {
      compression: {
        title: '内置上下文压缩',
        desc: '自动精简重复上下文，减少多余 token 消耗，用得越多、省得越多'
      },
      pricing: {
        title: '低至官方价格数倍',
        desc: 'Claude / GPT / Gemini 全线支持，最高可省 97%'
      },
      groupSwitch: {
        title: '多线路稳定切换',
        desc: '账号池自动调度，单个账号触发限流不影响使用'
      }
    },
    // 用户痛点区块
    painPoints: {
      title: '你是否也遇到这些问题？',
      items: {
        expensive: {
          title: '订阅费用高',
          desc: '每个 AI 服务都要单独订阅，每月支出越来越多'
        },
        complex: {
          title: '多账号难管理',
          desc: '不同平台的账号、密钥分散各处，管理起来很麻烦'
        },
        unstable: {
          title: '服务不稳定',
          desc: '单一账号容易触发限制，影响正常使用'
        },
        noControl: {
          title: '用量无法控制',
          desc: '不知道钱花在哪了，也无法限制团队成员的使用'
        }
      }
    },
    // 解决方案区块
    solutions: {
      title: '我们帮你解决',
      subtitle: '简单三步，开始省心使用 AI'
    },
    // 优势对比
    comparison: {
      title: '为什么选择我们？',
      headers: {
        feature: '对比项',
        official: '官方订阅',
        us: '本平台'
      },
      items: {
        pricing: {
          feature: '付费方式',
          official: '固定月费，用不完也付',
          us: '按量付费，用多少付多少'
        },
        models: {
          feature: '模型选择',
          official: '单一服务商',
          us: '多模型随意切换'
        },
        management: {
          feature: '账号管理',
          official: '每个服务单独管理',
          us: '统一密钥，一站管理'
        },
        stability: {
          feature: '服务稳定性',
          official: '单账号易触发限制',
          us: '多账号池，自动切换'
        },
        control: {
          feature: '用量控制',
          official: '无法限制',
          us: '可设配额、查明细'
        }
      }
    },
    providers: {
      title: '已支持的 AI 模型',
      description: '一个客户端，多种选择，价格仅为官方倍率',
      supported: '已支持',
      soon: '即将推出',
      priceFrom: '低至官方 {rate} 倍',
      claude: 'Claude',
      gpt: 'GPT',
      gemini: 'Gemini',
      more: '更多'
    },
    // CTA 区块
    cta: {
      title: '现在就开始，几分钟就能用上',
      description: '下载客户端，注册登录、分组切换、余额充值都在里面',
      button: '下载客户端'
    },
    footer: {
      allRightsReserved: '保留所有权利。'
    }
  },

  // Key Usage Query Page
  keyUsage: {
    title: 'API Key 用量查询',
    subtitle: '输入您的 API Key 以查看实时消费金额与使用状态',
    placeholder: 'sk-ant-mirror-xxxxxxxxxxxx',
    query: '查询',
    querying: '查询中...',
    privacyNote: '您的 Key 仅在浏览器本地处理，不会被存储',
    dateRange: '统计范围:',
    dateRangeToday: '今日',
    dateRange7d: '7 天',
    dateRange30d: '30 天',
    dateRange90d: '90 天',
    dateRangeCustom: '自定义',
    apply: '应用',
    used: '已使用',
    detailInfo: '详细信息',
    tokenStats: 'Token 统计',
    dailyDetail: '按日明细',
    modelStats: '模型用量统计',
    // Table headers
    date: '日期',
    model: '模型',
    requests: '请求数',
    inputTokens: '输入 Tokens',
    outputTokens: '输出 Tokens',
    cacheCreationTokens: '缓存创建',
    cacheReadTokens: '缓存读取',
    cacheWriteTokens: '缓存写入',
    totalTokens: '总 Tokens',
    cost: '费用',
    // Status
    quotaMode: 'Key 限额模式',
    walletBalance: '钱包余额',
    // Ring card titles
    totalQuota: '总额度',
    limit5h: '5 小时限额',
    limitDaily: '日限额',
    limit7d: '7 天限额',
    limitWeekly: '周限额',
    limitMonthly: '月限额',
    // Detail rows
    remainingQuota: '剩余额度',
    expiresAt: '过期时间',
    todayExpires: '(今日到期)',
    daysLeft: '({days} 天)',
    usedQuota: '已用额度',
    resetNow: '即将重置',
    subscriptionType: '订阅类型',
    billingType: '计费方式',
    subscriptionExpires: '订阅到期',
    // Usage stat cells
    todayRequests: '今日请求',
    todayInputTokens: '今日输入',
    todayOutputTokens: '今日输出',
    todayTokens: '今日 Tokens',
    todayCacheCreation: '今日缓存创建',
    todayCacheRead: '今日缓存读取',
    todayCost: '今日费用',
    rpmTpm: 'RPM / TPM',
    totalRequests: '累计请求',
    totalInputTokens: '累计输入',
    totalOutputTokens: '累计输出',
    totalTokensLabel: '累计 Tokens',
    totalCacheCreation: '累计缓存创建',
    totalCacheRead: '累计缓存读取',
    totalCost: '累计费用',
    avgDuration: '平均耗时',
    // Messages
    enterApiKey: '请输入 API Key',
    querySuccess: '查询成功',
    queryFailed: '查询失败',
    queryFailedRetry: '查询失败，请稍后重试',
    noDailyUsage: '暂无按日用量数据',
  },

  // Setup Wizard
  setup: {
    title: 'Sub2API 安装向导',
    description: '配置您的 Sub2API 实例',
    database: {
      title: '数据库配置',
      description: '连接到您的 PostgreSQL 数据库',
      host: '主机',
      port: '端口',
      username: '用户名',
      password: '密码',
      databaseName: '数据库名称',
      sslMode: 'SSL 模式',
      passwordPlaceholder: '密码',
      ssl: {
        disable: '禁用',
        require: '要求',
        verifyCa: '验证 CA',
        verifyFull: '完全验证'
      }
    },
    redis: {
      title: 'Redis 配置',
      description: '连接到您的 Redis 服务器',
      host: '主机',
      port: '端口',
      username: '用户名（可选）',
      password: '密码（可选）',
      database: '数据库',
      usernamePlaceholder: '默认用户留空',
      passwordPlaceholder: '密码',
      enableTls: '启用 TLS',
      enableTlsHint: '连接 Redis 时使用 TLS（公共 CA 证书）'
    },
    admin: {
      title: '管理员账户',
      description: '创建您的管理员账户',
      email: '邮箱',
      password: '密码',
      confirmPassword: '确认密码',
      passwordPlaceholder: '至少 8 个字符',
      confirmPasswordPlaceholder: '确认密码',
      passwordMismatch: '密码不匹配'
    },
    ready: {
      title: '准备安装',
      description: '检查您的配置并完成安装',
      database: '数据库',
      redis: 'Redis',
      adminEmail: '管理员邮箱'
    },
    status: {
      testing: '测试中...',
      success: '连接成功',
      testConnection: '测试连接',
      installing: '安装中...',
      completeInstallation: '完成安装',
      completed: '安装完成！',
      redirecting: '正在跳转到登录页面...',
      restarting: '服务正在重启，请稍候...',
      timeout: '服务重启时间超出预期，请手动刷新页面。'
    }
  },

  // Common
}
