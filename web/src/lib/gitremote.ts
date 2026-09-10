if (value.includes("::")) {
  return "That looks like a git remote helper, which git would execute. Use an https:// or ssh:// URL.";
}