//! 一道自动闸门：**工作台树里不许有被 git 忽略的源码文件**。
//!
//! # 为什么需要它
//!
//! 仓库根的 `.gitignore` 里有几条**裸规则**（`tests`、`scripts`、`/tools/**/bin/`），
//! 它们会匹配任意深度的同名目录。本意是挡 .NET 的构建产物和别处的脚本，
//! 但 Rust 的 `tests/`、`src/bin/` 和我们的 `scripts/` 正好撞上去。
//!
//! 后果**不是报错，而是静默**：`git add` 一声不吭，文件就是没进去。这已经发生过三次
//! （A1 的 fixture 与捕获脚本、A4 的探针二进制、A5 的桥测试），每次都是靠「提交前记得
//! 逐个核对暂存清单」才发现的。其中最贵的一次差点丢掉三份**要花真实中转站调用才能重录**
//! 的报文。
//!
//! 靠记性的防线迟早会漏，所以把它变成一条测试。**加了新目录形状而这条测试挂了，
//! 说明该往根 `.gitignore` 里补一条放行，不是把这条测试删掉。**

use std::path::{Path, PathBuf};
use std::process::Command;

/// 值得进版本库的东西：源码、清单、录制报文、导出的 schema。
fn is_source(path: &Path) -> bool {
    matches!(
        path.extension().and_then(|e| e.to_str()),
        Some("rs" | "py" | "toml" | "jsonl" | "json" | "lock")
    )
}

/// 构建产物与生成物，本来就该被忽略。
fn is_build_output(path: &Path) -> bool {
    path.components().any(|c| {
        matches!(
            c.as_os_str().to_str(),
            Some("target") | Some("gen") | Some("node_modules")
        )
    })
}

fn collect(dir: &Path, out: &mut Vec<PathBuf>) {
    let Ok(entries) = std::fs::read_dir(dir) else { return };
    for entry in entries.flatten() {
        let path = entry.path();
        if is_build_output(&path) {
            continue;
        }
        if path.is_dir() {
            collect(&path, out);
        } else if is_source(&path) {
            out.push(path);
        }
    }
}

#[test]
fn no_workbench_source_file_is_silently_gitignored() {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR"));

    let mut files = Vec::new();
    collect(&root, &mut files);
    assert!(
        files.len() > 20,
        "只找到 {} 个源码文件，遍历大概出问题了",
        files.len()
    );

    // **路径必须走 stdin，不能拼进命令行。**
    // `protocol/` 下光 schema 就有两百多个文件，拼成一条命令会撞上 Windows 32K 的
    // 命令行长度上限；那时 `Command` 直接起不来，而第一版把这个失败当成了「没有 git」
    // 静默跳过 —— 于是闸门一直显示绿色却什么都没检查。是反向对照把它抓出来的。
    let mut child = Command::new("git")
        .args(["check-ignore", "--stdin"])
        .current_dir(&root)
        .stdin(std::process::Stdio::piped())
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()
        .expect("起不了 git —— 这条闸门需要 git 才能工作，不要让它静默跳过");

    {
        use std::io::Write;
        let mut stdin = child.stdin.take().expect("拿不到 git 的 stdin");
        for file in &files {
            writeln!(stdin, "{}", file.display()).expect("写路径给 git");
        }
        // 关掉 stdin 给 git 一个 EOF，否则它会一直等。
    }

    let output = child.wait_with_output().expect("等 git 退出");
    // `check-ignore` 命中时返回 0、无命中返回 1，**两种都是正常结果**；
    // 大于 1 才是它自己出错了。
    let code = output.status.code().unwrap_or(-1);
    assert!(
        code == 0 || code == 1,
        "git check-ignore 自己出错了（退出码 {code}）：{}",
        String::from_utf8_lossy(&output.stderr)
    );

    let ignored: Vec<String> = String::from_utf8_lossy(&output.stdout)
        .lines()
        .map(str::trim)
        .filter(|l| !l.is_empty())
        // git 会把含反斜杠的路径加引号并转义，读起来很难受，还原一下。
        .map(|l| l.trim_matches('"').replace("\\\\", "\\"))
        .collect();

    assert!(
        ignored.is_empty(),
        "下面这些源码文件被 .gitignore 静默吞掉了，`git add` 不会报错但也不会加进去：\n  {}\n\
         \n修法是往仓库根 .gitignore 里补一条 `!` 放行（旁边已经有几条同类的），\
         不是把这条测试删掉。",
        ignored.join("\n  ")
    );
}
