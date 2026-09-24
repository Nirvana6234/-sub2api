export default {
  guestTrial: {
    pageTitle: 'Free trial',
    eyebrow: 'Web AI',
    continueTrial: 'Keep trying it',
    backHome: 'Back to home',
    noSignup: 'No sign-up',
    title: 'Start chatting, no account needed',
    subtitle: 'Type a question and chat with AI. The trial is text-only; sign up to unlock image generation and editing, file uploads and every model.',
    login: 'Log in',
    register: 'Sign up free',
    registerFree: 'Sign up free to unlock everything',
    openWorkspace: 'Open Web Workspace',
    model: 'Model',
    remaining: '{remaining} / {total} trial messages left today',
    emptyHint: 'Ask anything, for example:',
    suggestion1: 'Write a polite email asking for a day off',
    suggestion2: 'Explain what an AI gateway is in plain words',
    suggestion3: 'Give me a one-week workout plan',
    placeholder: 'Type your question. Enter to send, Shift + Enter for a new line',
    textOnly: 'The trial supports text chat only',
    send: 'Send',
    stop: 'Stop',
    emptyAnswer: '(No reply this time. Try rephrasing.)',
    sendFailed: 'Failed to send. Please try again later.',
    captchaFirst: 'Please complete the captcha above first',
    captchaFailed: 'Captcha check failed. Please try again.',
    exhaustedTitle: "You've used today's trial messages",
    exhaustedHint: 'Sign up for a free account to keep chatting, plus image generation, editing and file uploads.',
    disabledTitle: 'The web trial is not open yet',
    disabledHint: 'Sign up or log in to use the full Web Workspace.',
    unlockTitle: 'After signing up you can also',
    unlock1: 'Generate images, or upload one and edit it',
    unlock2: 'Upload files and let AI read them',
    unlock3: 'Switch between every model',
    unlock4: 'Use it in Codex and other plugins'
  },
  batchImageGuide: {
    title: 'Batch Image Generation',
    description: 'Submit multiple prompts in one job and download the generated images when complete'
  },
  // Home Page
  homeIntro: {
    eyebrow: 'AI gateway · Web AI',
    eyebrowGatewayOnly: 'AI gateway · Many models',
    title: 'Many models,',
    titleAccent: 'all inside Codex.',
    description: 'We are an AI gateway: GPT, Claude, Gemini and more reach Codex through a single endpoint, and work in Claude Code, Cursor, Cline, Cherry Studio and other plugins.',
    descriptionWeb: ' Prefer not to install anything? Chat, generate and edit images right in your browser.',
    ctaStart: 'Sign in / Sign up',
    ctaStartWork: 'Start working',
    ctaConsole: 'Open console',
    ctaTrial: 'Try it free',
    ctaTrialBadge: 'No sign-up',
    trialHint: 'No account needed. Open it and start chatting.',
    ctaWeb: 'Open Web Workspace',
    ctaClient: 'Download client',
    ctaClientPrimary: 'Download the client',
    beginnerNote: 'One account, one key, one balance for every option.',
    ideaNote: 'GPT, Claude, Gemini: one key connects them all.',
    purpose: 'We connect the models and handle your account and billing; you describe what you need in Codex, a plugin, or the Web Workspace.',
    purposeNoWeb: 'We connect the models and handle your account and billing; you describe what you need in Codex or a plugin.',
    map: {
      title: 'How it connects',
      hint: 'One endpoint brings every model to the tools you already use',
      models: 'MODELS',
      more: 'and more',
      gateway: '{name} gateway',
      protocols: 'OpenAI · Anthropic · Gemini compatible',
      targets: 'USE IT IN',
      codexHint: 'CLI · desktop',
      webHint: 'In your browser',
      fact1: 'One endpoint',
      fact2: 'One key for every model',
      fact3: 'One balance, pay as you go'
    },
    ways: {
      eyebrow: 'How to use it',
      title: 'Pick the way\nthat suits you',
      note: 'Every option shares the same account and balance. Use them together if you like.',
      api: {
        title: 'Codex and other plugins',
        p1: 'Switch between GPT, Claude and more in Codex CLI or Codex desktop.',
        p2: 'Claude Code, Cursor, Cline, Cherry Studio and any tool that speaks the OpenAI, Anthropic or Gemini API.',
        action: 'Create a key'
      },
      web: {
        title: 'Web AI',
        p1: 'Runs in your browser. Nothing to install.',
        p2: 'Chat, generate images, upload one and edit it, plus an infinite canvas and gallery.',
        action: 'Open Web Workspace'
      },
      client: {
        title: 'Desktop Assistant',
        badge: 'Recommended',
        p1: 'The download page has both Codex and our client. Install them and you are ready, no config files to edit.',
        p2: 'See your account, group and balance at a glance. It also saves tokens, and shows your usage and savings.',
        action: 'Download client',
        guide: 'Install guide'
      }
    },
    codex: {
      eyebrow: 'Use it in Codex',
      title: 'Three steps to connect Codex.',
      step1Title: 'Create a key',
      step1: 'Sign in and create one under API Keys. One key works for every model.',
      step2Title: 'Add it to your Codex config',
      step2: 'Put this in config.toml. The full version is under Use Key on the keys page, ready to copy.',
      step3Title: 'Open Codex and get to work',
      step3: 'Switch between GPT, Claude and more in the model list, then describe what you want.',
      comment: 'Endpoint filled in for this site',
      action: 'Create a key',
      clientTitle: 'Three steps to start working in Codex.',
      clientStep1Title: 'Download two apps',
      clientStep1: 'The download page has both our client and Codex. Pick the version for your computer.',
      clientStep2Title: 'Sign in to the client',
      clientStep2: 'Once you sign in, the client connects Codex for you. No config files to edit.',
      clientStep3Title: 'Open Codex and get to work',
      clientStep3: 'Describe what you want. The client saves tokens and keeps your usage visible.',
      clientAction: 'Download the client',
      guide: 'Install guide',
      manualToggle: 'Prefer manual setup? See the config.toml'
    }
  },
  home: {
    viewOnGithub: 'View on GitHub',
    viewDocs: 'View Documentation',
    docs: 'Docs',
    switchToLight: 'Switch to Light Mode',
    switchToDark: 'Switch to Dark Mode',
    dashboard: 'Dashboard',
    login: 'Login',
    getStarted: 'Get Started',
    register: 'Sign up',
    goToDashboard: 'Go to Dashboard',
    downloadClient: 'Download the app',
    loginExisting: 'Already have an account? Log in',
    // User-focused value proposition
    heroSubtitle: 'One app for Claude, GPT, and Gemini',
    heroDescription: 'Sign up, switch groups, and top up your balance — all inside the app, ready to go',
    tags: {
      allInOne: 'Sign up and top up in the app',
      groupSwitch: 'Reliable group switching',
      payAsYouGo: 'Pay as you go'
    },
    benefits: {
      compression: {
        title: 'Built-in context compression',
        desc: 'Trims redundant context automatically to cut token usage — the more you use, the more you save'
      },
      pricing: {
        title: 'A fraction of official pricing',
        desc: 'Claude / GPT / Gemini all supported, save up to 97%'
      },
      groupSwitch: {
        title: 'Reliable multi-route switching',
        desc: 'Account pool auto-scheduling keeps you online even if one account gets rate-limited'
      }
    },
    // Pain points section
    painPoints: {
      title: 'Sound Familiar?',
      items: {
        expensive: {
          title: 'High Subscription Costs',
          desc: 'Paying for multiple AI subscriptions that add up every month'
        },
        complex: {
          title: 'Account Chaos',
          desc: 'Managing scattered accounts and API keys across different platforms'
        },
        unstable: {
          title: 'Service Interruptions',
          desc: 'Single accounts hitting rate limits and disrupting your workflow'
        },
        noControl: {
          title: 'No Usage Control',
          desc: "Can't track where your money goes or limit team member usage"
        }
      }
    },
    // Solutions section
    solutions: {
      title: 'We Solve These Problems',
      subtitle: 'Three simple steps to stress-free AI access'
    },
    // Comparison section
    comparison: {
      title: 'Why Choose Us?',
      headers: {
        feature: 'Comparison',
        official: 'Official Subscriptions',
        us: 'Our Platform'
      },
      items: {
        pricing: {
          feature: 'Pricing',
          official: 'Fixed monthly fee, pay even if unused',
          us: 'Pay only for what you use'
        },
        models: {
          feature: 'Model Selection',
          official: 'Single provider only',
          us: 'Switch between models freely'
        },
        management: {
          feature: 'Account Management',
          official: 'Manage each service separately',
          us: 'Unified key, one dashboard'
        },
        stability: {
          feature: 'Stability',
          official: 'Single account rate limits',
          us: 'Multi-account pool, auto-failover'
        },
        control: {
          feature: 'Usage Control',
          official: 'Not available',
          us: 'Quotas & detailed analytics'
        }
      }
    },
    providers: {
      title: 'Supported AI Models',
      description: 'One app, multiple choices, at a fraction of official pricing',
      supported: 'Supported',
      soon: 'Soon',
      priceFrom: 'From {rate}x official',
      claude: 'Claude',
      gpt: 'GPT',
      gemini: 'Gemini',
      more: 'More'
    },
    // CTA section
    cta: {
      title: 'Get started in minutes',
      description: 'Download the app — sign up, switch groups, and top up, all in one place',
      button: 'Download the app'
    },
    footer: {
      allRightsReserved: 'All rights reserved.'
    }
  },

  // Key Usage Query Page
  keyUsage: {
    title: 'API Key Usage',
    subtitle: 'Enter your API Key to view real-time spending and usage status',
    placeholder: 'sk-ant-mirror-xxxxxxxxxxxx',
    query: 'Query',
    querying: 'Querying...',
    privacyNote: 'Your Key is processed locally in the browser and will not be stored',
    dateRange: 'Date Range:',
    dateRangeToday: 'Today',
    dateRange7d: '7 Days',
    dateRange30d: '30 Days',
    dateRange90d: '90 Days',
    dateRangeCustom: 'Custom',
    apply: 'Apply',
    used: 'Used',
    detailInfo: 'Detail Information',
    tokenStats: 'Token Statistics',
    dailyDetail: 'Daily Detail',
    modelStats: 'Model Usage Statistics',
    // Table headers
    date: 'Date',
    model: 'Model',
    requests: 'Requests',
    inputTokens: 'Input Tokens',
    outputTokens: 'Output Tokens',
    cacheCreationTokens: 'Cache Creation',
    cacheReadTokens: 'Cache Read',
    cacheWriteTokens: 'Cache Write',
    totalTokens: 'Total Tokens',
    cost: 'Cost',
    // Status
    quotaMode: 'Key Quota Mode',
    walletBalance: 'Wallet Balance',
    // Ring card titles
    totalQuota: 'Total Quota',
    limit5h: '5-Hour Limit',
    limitDaily: 'Daily Limit',
    limit7d: '7-Day Limit',
    limitWeekly: 'Weekly Limit',
    limitMonthly: 'Monthly Limit',
    // Detail rows
    remainingQuota: 'Remaining Quota',
    expiresAt: 'Expires At',
    todayExpires: '(expires today)',
    daysLeft: '({days} days)',
    usedQuota: 'Used Quota',
    resetNow: 'Resetting soon',
    subscriptionType: 'Subscription Type',
    billingType: 'Billing Type',
    subscriptionExpires: 'Subscription Expires',
    // Usage stat cells
    todayRequests: 'Today Requests',
    todayInputTokens: 'Today Input',
    todayOutputTokens: 'Today Output',
    todayTokens: 'Today Tokens',
    todayCacheCreation: 'Today Cache Creation',
    todayCacheRead: 'Today Cache Read',
    todayCost: 'Today Cost',
    rpmTpm: 'RPM / TPM',
    totalRequests: 'Total Requests',
    totalInputTokens: 'Total Input',
    totalOutputTokens: 'Total Output',
    totalTokensLabel: 'Total Tokens',
    totalCacheCreation: 'Total Cache Creation',
    totalCacheRead: 'Total Cache Read',
    totalCost: 'Total Cost',
    avgDuration: 'Avg Duration',
    // Messages
    enterApiKey: 'Please enter an API Key',
    querySuccess: 'Query successful',
    queryFailed: 'Query failed',
    queryFailedRetry: 'Query failed, please try again later',
    noDailyUsage: 'No daily usage data',
  },

  // Setup Wizard
  setup: {
    title: 'Sub2API Setup',
    description: 'Configure your Sub2API instance',
    database: {
      title: 'Database Configuration',
      description: 'Connect to your PostgreSQL database',
      host: 'Host',
      port: 'Port',
      username: 'Username',
      password: 'Password',
      databaseName: 'Database Name',
      sslMode: 'SSL Mode',
      passwordPlaceholder: 'Password',
      ssl: {
        disable: 'Disable',
        require: 'Require',
        verifyCa: 'Verify CA',
        verifyFull: 'Verify Full'
      }
    },
    redis: {
      title: 'Redis Configuration',
      description: 'Connect to your Redis server',
      host: 'Host',
      port: 'Port',
      username: 'Username (optional)',
      password: 'Password (optional)',
      database: 'Database',
      usernamePlaceholder: 'Leave empty for default user',
      passwordPlaceholder: 'Password',
      enableTls: 'Enable TLS',
      enableTlsHint: 'Use TLS when connecting to Redis (public CA certs)'
    },
    admin: {
      title: 'Admin Account',
      description: 'Create your administrator account',
      email: 'Email',
      password: 'Password',
      confirmPassword: 'Confirm Password',
      passwordPlaceholder: 'Min 8 characters',
      confirmPasswordPlaceholder: 'Confirm password',
      passwordMismatch: 'Passwords do not match'
    },
    ready: {
      title: 'Ready to Install',
      description: 'Review your configuration and complete setup',
      database: 'Database',
      redis: 'Redis',
      adminEmail: 'Admin Email'
    },
    status: {
      testing: 'Testing...',
      success: 'Connection Successful',
      testConnection: 'Test Connection',
      installing: 'Installing...',
      completeInstallation: 'Complete Installation',
      completed: 'Installation completed!',
      redirecting: 'Redirecting to login page...',
      restarting: 'Service is restarting, please wait...',
      timeout: 'Service restart is taking longer than expected. Please refresh the page manually.'
    }
  },

  // Common
}
