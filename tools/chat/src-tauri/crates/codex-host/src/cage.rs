//! 进程笼：**Chat 一死，codex 必须跟着死**。
//!
//! # 为什么非要有这东西
//!
//! 「关掉 Chat＝手机就够不着了」是整个远程操作方案的安全前提。如果关掉 Chat 之后
//! codex 还活着，这句话就是假的 —— 而且是那种平时看不出来、出事时才发现的假。
//!
//! # 两个机制，各管一半，谁也替不了谁
//!
//! | | 覆盖什么 | 不覆盖什么 |
//! |---|---|---|
//! | `kill_on_drop(true)` | 正常退出、panic 展开、`Child` 被丢弃 | **`TerminateProcess`** —— 析构函数根本不会跑 |
//! | Job Object（本模块） | **任务管理器里「结束任务」这种强杀** | 无（这是兜底） |
//!
//! 两个都要。以后看着觉得其中一个多余而删掉，删的就是「强杀」这条路上的唯一防线。
//!
//! # 平台
//!
//! **笼子的保证目前只在 Windows 成立。** 非 Windows 上只有 `kill_on_drop`，
//! 那是个明显更弱的东西 —— macOS 没有等价物，Linux 有 `PR_SET_PDEATHSIG` 但只对
//! 直接子进程有效。别把这里的 `#[cfg]` 当成跨平台对等。

use std::io;

/// 一个把子进程关进去的笼子。**笼子被销毁（含进程被强杀）时，里面的进程全部被杀。**
pub struct Cage {
    #[cfg(windows)]
    job: imp::Job,
}

impl Cage {
    /// 建一个新笼子。
    ///
    /// # Errors
    /// 系统调用失败时返回。
    pub fn new() -> io::Result<Self> {
        Ok(Cage {
            #[cfg(windows)]
            job: imp::Job::new()?,
        })
    }

    /// 把一个刚起来的子进程关进笼子。
    ///
    /// # 有个小窗口
    ///
    /// 从 `spawn` 返回到这里执行完之间，子进程理论上能生出不在笼子里的孙进程。
    /// 严格的做法是 `CREATE_SUSPENDED` 起进程、入笼、再 `ResumeThread`，但拿到主线程
    /// 句柄要绕开 std 的 `Command` 自己调 `CreateProcess`。
    ///
    /// 我们选了不绕：codex app-server 起来后**先读 stdin**，真正会生孙进程（跑命令、
    /// 沙箱助手）是一轮对话开始以后的事，离入笼已经很远。**这个取舍是明写的，不是忘了。**
    ///
    /// # Errors
    /// 拿不到进程句柄或系统调用失败时返回。
    pub fn adopt(&self, child: &tokio::process::Child) -> io::Result<()> {
        #[cfg(windows)]
        {
            let handle = child
                .raw_handle()
                .ok_or_else(|| io::Error::other("子进程已经退出，拿不到句柄"))?;
            self.job.assign(handle)
        }
        #[cfg(not(windows))]
        {
            let _ = child;
            Ok(())
        }
    }

    /// 笼子是不是真的会在关闭时杀掉里面的进程。
    ///
    /// 存在这个函数是因为 `SetInformationJobObject` **设错结构体字段会静默成功** ——
    /// 于是笼子看着建好了、测试也过了，直到线上强杀时才发现根本没关住。
    /// 所以建完就回头查一次。
    pub fn kills_on_close(&self) -> io::Result<bool> {
        #[cfg(windows)]
        {
            self.job.kills_on_close()
        }
        #[cfg(not(windows))]
        {
            // 非 Windows 没有笼子，如实说「不保证」，别让调用方以为有。
            Ok(false)
        }
    }
}

#[cfg(windows)]
mod imp {
    use std::io;
    use std::os::windows::io::RawHandle;

    use windows_sys::Win32::Foundation::{CloseHandle, HANDLE};
    use windows_sys::Win32::System::JobObjects::{
        AssignProcessToJobObject, CreateJobObjectW, JobObjectExtendedLimitInformation,
        QueryInformationJobObject, SetInformationJobObject,
        JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
    };

    pub struct Job(HANDLE);

    // 句柄可以跨线程用；Job 只是它的所有者。
    unsafe impl Send for Job {}
    unsafe impl Sync for Job {}

    impl Job {
        pub fn new() -> io::Result<Self> {
            // SAFETY: 两个参数都传空，是文档允许的「默认安全属性、匿名 job」。
            let handle = unsafe { CreateJobObjectW(std::ptr::null(), std::ptr::null()) };
            if handle.is_null() {
                return Err(io::Error::last_os_error());
            }
            let job = Job(handle);

            let mut info: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = unsafe { std::mem::zeroed() };
            // 这一行是整个模块的重点：标志位在 BasicLimitInformation 里面，
            // 不在外层结构体上。设错地方不会报错，只会不生效。
            info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;

            // SAFETY: info 是刚初始化的对应类型，长度按它自己算。
            let ok = unsafe {
                SetInformationJobObject(
                    job.0,
                    JobObjectExtendedLimitInformation,
                    std::ptr::addr_of!(info).cast(),
                    std::mem::size_of::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
                )
            };
            if ok == 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(job)
        }

        pub fn assign(&self, process: RawHandle) -> io::Result<()> {
            // SAFETY: process 来自还活着的子进程。
            let ok = unsafe { AssignProcessToJobObject(self.0, process as HANDLE) };
            if ok == 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(())
        }

        /// 回头把标志位查出来核对一遍。
        pub fn kills_on_close(&self) -> io::Result<bool> {
            let mut info: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = unsafe { std::mem::zeroed() };
            let mut returned: u32 = 0;
            // SAFETY: 输出缓冲区就是 info 本身，长度按类型算。
            let ok = unsafe {
                QueryInformationJobObject(
                    self.0,
                    JobObjectExtendedLimitInformation,
                    std::ptr::addr_of_mut!(info).cast(),
                    std::mem::size_of::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
                    &mut returned,
                )
            };
            if ok == 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(info.BasicLimitInformation.LimitFlags & JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE != 0)
        }
    }

    impl Drop for Job {
        fn drop(&mut self) {
            // 关掉最后一个句柄 = 笼子关闭 = 里面的进程被杀。
            // 进程被强杀时，内核替我们关句柄 —— 这正是笼子比析构函数可靠的地方。
            unsafe {
                CloseHandle(self.0);
            }
        }
    }
}
