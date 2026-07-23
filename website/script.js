const header = document.querySelector("[data-header]");
const menuButton = document.querySelector("[data-menu-button]");
const navigation = document.querySelector("[data-nav]");
const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

function updateHeader() {
  header?.classList.toggle("scrolled", window.scrollY > 18);
}

function closeMenu() {
  navigation?.classList.remove("open");
  menuButton?.setAttribute("aria-expanded", "false");
  document.body.classList.remove("menu-open");
}

menuButton?.addEventListener("click", () => {
  const isOpen = menuButton.getAttribute("aria-expanded") === "true";
  menuButton.setAttribute("aria-expanded", String(!isOpen));
  navigation?.classList.toggle("open", !isOpen);
  document.body.classList.toggle("menu-open", !isOpen);
});

navigation?.querySelectorAll("a").forEach((link) => {
  link.addEventListener("click", closeMenu);
});

window.addEventListener("resize", () => {
  if (window.innerWidth > 760) closeMenu();
});

window.addEventListener("scroll", updateHeader, { passive: true });
updateHeader();

const revealItems = document.querySelectorAll(".reveal");

if (reduceMotion || !("IntersectionObserver" in window)) {
  revealItems.forEach((item) => item.classList.add("visible"));
} else {
  const observer = new IntersectionObserver(
    (entries) => {
      entries.forEach((entry) => {
        if (!entry.isIntersecting) return;
        entry.target.classList.add("visible");
        observer.unobserve(entry.target);
      });
    },
    { threshold: 0.12, rootMargin: "0px 0px -40px" },
  );

  revealItems.forEach((item) => observer.observe(item));
}

const screenshotTabs = document.querySelectorAll("[data-screenshot]");
const productImage = document.querySelector("[data-product-image]");
const productTitle = document.querySelector("[data-product-title]");
const productDescription = document.querySelector("[data-product-description]");

screenshotTabs.forEach((tab) => {
  tab.addEventListener("click", () => {
    if (tab.classList.contains("active") || !productImage) return;

    screenshotTabs.forEach((item) => {
      const isActive = item === tab;
      item.classList.toggle("active", isActive);
      item.setAttribute("aria-selected", String(isActive));
    });

    productImage.classList.add("switching");
    const nextImage = new Image();
    nextImage.src = tab.dataset.screenshot;
    nextImage.addEventListener("load", () => {
      productImage.src = nextImage.src;
      productImage.alt = `GMHA ${tab.dataset.title}控制台`;
      if (productTitle) productTitle.textContent = tab.dataset.title;
      if (productDescription) productDescription.textContent = tab.dataset.description;
      productImage.classList.remove("switching");
    });
  });
});
