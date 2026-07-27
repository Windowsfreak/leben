// Digital Business Card Frontend Logic

// App State
let q = '';
let lang = 'de';
let offset = 0;
const limit = 12;
let loading = false;
let hasMore = true;
let isAdmin = false;
let observer = null;

// Dynamic configuration state
let appConfig = null;
let adminScriptLoaded = false;

// Expose state flags to prevent ReferenceErrors in page-unload listeners
window.isEditing = false;
window.isSettingsEditing = false;
window.monacoLoaded = false;
window.adminScriptLoaded = false;

// Wallpaper collection
const wallpapers = [
    // Leave empty for CSS gradient fallback.
    // If you put absolute paths to wallpaper images here, they will be chosen randomly.
];

// Initialize application on DOM content load
document.addEventListener('DOMContentLoaded', () => {
    // Initialize light/dark theme preference
    initTheme();
    
    // Load dynamic configuration (hero, suggestions, wallpapers)
    loadConfig(() => {
        // Check administrative status
        checkAdminStatus();
        
        // Load initial tiles
        resetAndLoad();
        
        // Setup event handlers
        setupEventHandlers();

        // Handle vanity URL / hashtag routing on initial load
        handleURLRouting();
    });


    // Parallax scroll variable updater (calculates 0 to 1 scrollPercent and scrollY)
    const updateScrollVariables = () => {
        const scrollHeight = document.documentElement.scrollHeight - window.innerHeight;
        const scrollPercent = scrollHeight > 0 ? (window.scrollY / scrollHeight) : 0;
        document.body.style.setProperty('--scroll-percent', scrollPercent);
        document.body.style.setProperty('--scroll-y', `${window.scrollY}px`);
    };

    window.addEventListener('scroll', updateScrollVariables, { passive: true });
    window.addEventListener('resize', updateScrollVariables, { passive: true });
    
    // Initial run to align background
    updateScrollVariables();
});

// Detect browser preferred language, honoring the list of supported_languages and localStorage
function detectLanguage() {
    const savedLang = localStorage.getItem('lang');
    const supportedCodes = appConfig && appConfig.supported_languages ? Object.keys(appConfig.supported_languages) : [];
    
    if (savedLang && supportedCodes.includes(savedLang)) {
        lang = savedLang;
        updateLanguageUI();
        return;
    }

    const browserLang = (navigator.language || navigator.languages[0] || 'de').substring(0, 2).toLowerCase();
    
    if (supportedCodes.length > 0) {
        if (supportedCodes.includes(browserLang)) {
            lang = browserLang;
        } else {
            // Default to first supported language key
            lang = supportedCodes[0];
        }
    } else {
        // Fallback default
        lang = (browserLang === 'en' || browserLang === 'de') ? browserLang : 'de';
    }
    updateLanguageUI();
}

// Update Language specific UI elements
function updateLanguageUI() {
    const textEl = document.getElementById('currentLangText');
    if (textEl) {
        textEl.textContent = lang.toUpperCase();
    }
    
    // Update active class in dropdown items
    document.querySelectorAll('.dropdown-item').forEach(item => {
        if (item.getAttribute('data-lang') === lang) {
            item.classList.add('active');
        } else {
            item.classList.remove('active');
        }
    });
    
    // Localize placeholder texts from config hero section!
    const heroDetails = appConfig && appConfig.hero ? (appConfig.hero[lang] || appConfig.hero['de'] || {}) : {};
    
    const searchInput = document.getElementById('searchInput');
    if (searchInput) {
        searchInput.placeholder = heroDetails.searchPlaceholder || 
            (lang === 'en' ? "Search topics (e.g., IT, Finance, Parkour, Bio)..." : "Suche nach Themen (z.B. IT, Finanzen, Parkour, Bio)...");
    }
}

// Render dynamic language dropdown menu
function applyLanguagesConfig() {
    if (!appConfig || !appConfig.supported_languages) return;
    
    const menu = document.getElementById('langDropdownMenu');
    if (!menu) return;
    
    menu.innerHTML = '';
    
    Object.entries(appConfig.supported_languages).forEach(([code, name]) => {
        const btn = document.createElement('button');
        btn.className = 'dropdown-item';
        btn.setAttribute('data-lang', code);
        btn.textContent = name;
        menu.appendChild(btn);
    });
}

// Initialize theme state (dark mode by default, or respect saved preference / preferences media query)
function initTheme() {
    const savedTheme = localStorage.getItem('theme');
    const prefersLight = window.matchMedia('(prefers-color-scheme: light)').matches;
    const btn = document.getElementById('themeToggleBtn');
    
    if (savedTheme === 'light' || (!savedTheme && prefersLight)) {
        document.body.classList.add('light-theme');
        if (btn) btn.innerHTML = '<i class="fa-solid fa-sun"></i>';
    } else {
        document.body.classList.remove('light-theme');
        if (btn) btn.innerHTML = '<i class="fa-solid fa-moon"></i>';
    }
}

// Load dynamic page settings config
function loadConfig(callback) {
    fetch('config.json?v=' + Date.now())
        .then(res => res.json())
        .then(data => {
            appConfig = data;
            
            // Re-initialize wallpapers list
            wallpapers.length = 0;
            if (data.wallpapers && data.wallpapers.length > 0) {
                wallpapers.push(...data.wallpapers);
            }
            
            // Set wallpaper background
            setupWallpaper();
            
            // 1. Detect language (honoring supported_languages in config)
            detectLanguage();
            
            // 2. Render hero title/tagline
            applyHeroConfig();
            
            // 3. Render suggest list
            applySuggestionsConfig();
            
            // 4. Render dynamic language selector
            applyLanguagesConfig();
            
            if (callback) callback();
        })
        .catch(err => {
            console.error("Error loading config.json:", err);
            setupWallpaper();
            detectLanguage(); // Fallback language detection
            if (callback) callback();
        });
}

// Render dynamic hero details
function applyHeroConfig() {
    if (!appConfig || !appConfig.hero) return;
    const heroDetails = appConfig.hero[lang] || appConfig.hero['de'] || {};
    
    const titleEl = document.getElementById('heroTitle');
    const taglineEl = document.getElementById('heroTagline');
    
    if (titleEl) titleEl.textContent = heroDetails.title || 'Björn Eberhardt';
    if (taglineEl) taglineEl.textContent = heroDetails.tagline || 'Leben. Verstehen. Wachsen.';
}

// Render dynamic search suggestions
function applySuggestionsConfig() {
    if (!appConfig || !appConfig.suggestions) return;
    const suggs = appConfig.suggestions[lang] || appConfig.suggestions['de'] || {};
    
    const container = document.getElementById('categorySuggestions');
    if (!container) return;
    container.innerHTML = '';
    
    Object.entries(suggs).forEach(([text, query]) => {
        const pill = document.createElement('span');
        pill.className = 'suggestion-pill';
        pill.setAttribute('data-search', query);
        pill.textContent = `# ${text}`;
        
        pill.addEventListener('click', () => {
            const searchInput = document.getElementById('searchInput');
            searchInput.value = query;
            q = query;
            resetAndLoad();
            
            // Mark pill as active
            document.querySelectorAll('.suggestion-pill').forEach(p => p.classList.remove('active'));
            pill.classList.add('active');
        });
        
        container.appendChild(pill);
    });
}


// Setup background wallpaper
function setupWallpaper() {
    if (wallpapers.length > 0) {
        const randomWallpaper = wallpapers[Math.floor(Math.random() * wallpapers.length)];
        document.body.style.setProperty('--wallpaper-url', `url('${randomWallpaper}')`);
        document.body.classList.add('has-wallpaper');
    } else {
        document.body.style.removeProperty('--wallpaper-url');
        document.body.classList.remove('has-wallpaper');
    }
}

// Reset grid and reload from offset 0
function resetAndLoad() {
    offset = 0;
    hasMore = true;
    document.getElementById('tilesGrid').innerHTML = '';
    loadMore();
}

// Fetch and append more tiles
function loadMore() {
    if (loading || !hasMore) return;
    
    loading = true;
    const spinner = document.getElementById('loadingSpinner');
    spinner.classList.add('active');
    
    const url = `api.php?action=search&q=${encodeURIComponent(q)}&lang=${lang}&offset=${offset}&limit=${limit}`;
    
    fetch(url)
        .then(response => {
            if (!response.ok) {
                throw new Error(`API error: ${response.status}`);
            }
            return response.json();
        })
        .then(res => {
            if (res.status === 'success') {
                const tiles = res.data;
                const grid = document.getElementById('tilesGrid');
                
                tiles.forEach(tile => {
                    grid.appendChild(createTileElement(tile));
                });
                
                if (tiles.length < limit) {
                    hasMore = false;
                }
                
                offset += tiles.length;
            } else {
                console.error('Search failed:', res.message);
                hasMore = false;
            }
        })
        .catch(err => {
            console.error('Error loading tiles:', err);
            hasMore = false;
        })
        .finally(() => {
            loading = false;
            spinner.classList.remove('active');
        });
}

// Create the DOM element for a tile
function createTileElement(tile, isMini = false) {
    const tileDiv = document.createElement('div');
    tileDiv.className = 'tile';
    
    // Set custom CSS property for theme accent color from database
    tileDiv.style.setProperty('--tile-color', tile.accent_color || '#fbbf24');

    // Set custom background style if specified
    if (tile.background && tile.background.trim() !== '') {
        tileDiv.style.background = tile.background.trim();
    }

    // Opacity overlay for invisible tiles in admin mode
    if (!tile.visible) {
        tileDiv.classList.add('invisible-tile');
    }
    
    const contentWrapper = document.createElement('div');
    contentWrapper.className = 'tile-content';
    contentWrapper.innerHTML = tile.html_teaser || '';
    tileDiv.appendChild(contentWrapper);
    
    // Admin editing icons overlay
    if (isAdmin && !isMini && typeof window.renderAdminControls === 'function') {
        window.renderAdminControls(tileDiv, tile);
    }
    
    // Main tile click behavior
    tileDiv.addEventListener('click', () => {
        if (tile.type === 'link') {
            if (tile.link) {
                window.open(tile.link, '_blank');
            }
        } else {
            openLightbox(tile);
        }
    });
    
    return tileDiv;
}

let activeLightboxTile = null;
let isProgrammaticRouting = false;

// Format hash for tile depending on default app language vs tile language
function getHashForTile(tile) {
    const tileName = tile.name.toLowerCase();
    const tileLang = (tile.language || lang).toLowerCase();
    const defaultLang = lang.toLowerCase();
    
    if (tileLang !== defaultLang) {
        return `#${tileName}:${tileLang}`;
    }
    return `#${tileName}`;
}

// Parse current window URL (hash, pathname, search) for tile name and language
function parseURLForTile() {
    let tileName = '';
    let tileLang = null;

    // 1. Check Hash first (e.g. #support, #support:en, #contact:en, #en/contact)
    const rawHash = window.location.hash ? window.location.hash.substring(1) : '';
    if (rawHash) {
        let hashStr = rawHash.trim();
        try {
            hashStr = decodeURIComponent(hashStr);
        } catch (e) {}
        if (hashStr.startsWith('/')) hashStr = hashStr.substring(1);

        if (hashStr.includes(':')) {
            const parts = hashStr.split(':');
            tileName = parts[0].trim();
            tileLang = parts[1].trim().toLowerCase();
        } else if (hashStr.includes('/')) {
            const parts = hashStr.split('/').filter(Boolean);
            if (parts.length >= 2 && ['de', 'en'].includes(parts[0].toLowerCase())) {
                tileLang = parts[0].toLowerCase();
                tileName = parts[1];
            } else if (parts.length > 0) {
                tileName = parts[parts.length - 1];
            }
        } else {
            tileName = hashStr;
        }
    }

    // 2. Check Pathname if no hash tile found (e.g. /support, /leben/support, /de/support)
    if (!tileName) {
        const rawSegments = window.location.pathname.split('/').filter(Boolean);
        const pathSegments = rawSegments.map(s => {
            try {
                return decodeURIComponent(s);
            } catch (e) {
                return s;
            }
        });
        const ignoredPaths = ['leben', 'index.html', 'index.php'];
        const cleanSegments = pathSegments.filter(s => !ignoredPaths.includes(s.toLowerCase()));

        if (cleanSegments.length > 0) {
            if (cleanSegments.length >= 2 && ['de', 'en'].includes(cleanSegments[0].toLowerCase())) {
                tileLang = cleanSegments[0].toLowerCase();
                tileName = cleanSegments[1];
            } else {
                const candidate = cleanSegments[cleanSegments.length - 1];
                if (!['de', 'en'].includes(candidate.toLowerCase())) {
                    tileName = candidate;
                }
            }
        }
    }

    // 3. Search parameters override (e.g. ?lang=en)
    const urlParams = new URLSearchParams(window.location.search);
    if (urlParams.has('lang')) {
        const queryLang = urlParams.get('lang').trim().toLowerCase();
        if (queryLang) {
            tileLang = queryLang;
        }
    }

    if (tileName) {
        tileName = tileName.trim().toLowerCase();
    }

    return { name: tileName, lang: tileLang };
}

// Handle URL routing based on hash or pathname
function handleURLRouting() {
    if (isProgrammaticRouting) return;

    const route = parseURLForTile();
    
    if (route.name) {
        // "Language would be stuffed into the call, if not attached to the url"
        const fetchLang = route.lang || lang;
        
        // Skip re-fetching if active tile is already showing this tile and language
        if (activeLightboxTile && 
            activeLightboxTile.name.toLowerCase() === route.name.toLowerCase() && 
            activeLightboxTile.language === fetchLang) {
            return;
        }

        fetch(`api.php?action=get_tile&name=${encodeURIComponent(route.name)}&lang=${encodeURIComponent(fetchLang)}`)
            .then(res => res.json())
            .then(res => {
                if (res.status === 'success' && res.data) {
                    openLightbox(res.data, false);
                } else {
                    // Tile not found by exact name: close lightbox, put keyword into search input, and run search
                    const dialog = document.getElementById('detailsDialog');
                    if (dialog && dialog.open) {
                        activeLightboxTile = null;
                        dialog.close();
                    }
                    
                    const searchInput = document.getElementById('searchInput');
                    if (searchInput) {
                        searchInput.value = route.name;
                        q = route.name;
                        resetAndLoad();
                    }
                }
            })
            .catch(err => {
                console.error("Routing tile load failed:", err);
                const searchInput = document.getElementById('searchInput');
                if (searchInput) {
                    searchInput.value = route.name;
                    q = route.name;
                    resetAndLoad();
                }
            });
    } else {
        // No tile specified in URL: close lightbox if open
        const dialog = document.getElementById('detailsDialog');
        if (dialog && dialog.open) {
            activeLightboxTile = null;
            dialog.close();
        }
    }
}

// Open Lightbox details dialog
function openLightbox(tile, updateHistory = true) {
    activeLightboxTile = tile;
    const dialog = document.getElementById('detailsDialog');
    const header = document.getElementById('detailsDialogHeader');
    const body = document.getElementById('detailsDialogBody');
    
    header.textContent = tile.title;
    body.innerHTML = '<div style="display:flex; justify-content:center; padding: 2rem;"><div class="spinner active"></div></div>';
    
    if (!dialog.open) {
        dialog.showModal();
    }

    // Update history state for back-navigation support without triggering event loops
    if (updateHistory) {
        const newHash = getHashForTile(tile);
        if (window.location.hash !== newHash) {
            isProgrammaticRouting = true;
            history.pushState({ tileName: tile.name, lang: tile.language }, '', newHash);
            setTimeout(() => { isProgrammaticRouting = false; }, 0);
        }
    }
    
    const tryRenderEditButton = (tileObj, targetBody) => {
        if (window.adminScriptLoaded && typeof window.renderLightboxEditButton === 'function') {
            window.renderLightboxEditButton(tileObj, targetBody);
        }
    };

    // If it points to an HTML content file
    if (tile.content_file) {
        fetch(`contents/${tile.content_file}`)
            .then(res => {
                if (!res.ok) throw new Error("File not found");
                return res.text();
            })
            .then(html => {
                body.innerHTML = html;
                tryRenderEditButton(tile, body);
                loadSimilarTiles(tile.name, body);
            })
            .catch(err => {
                body.innerHTML = `
                    <div class="lightbox-article">
                        <h2>${tile.title}</h2>
                        <div class="article-body">
                            ${tile.html_teaser || ''}
                            <p style="color:var(--danger); margin-top: 1rem;"><i class="fa-solid fa-triangle-exclamation"></i> Inhaltsdatei "${tile.content_file}" konnte nicht geladen werden.</p>
                        </div>
                    </div>
                `;
                tryRenderEditButton(tile, body);
                loadSimilarTiles(tile.name, body);
            });
    } else {
        // Fallback to database html rendering
        body.innerHTML = `
            <div class="lightbox-article">
                <h2>${tile.title}</h2>
                <div class="article-body">
                    ${tile.html_teaser || ''}
                </div>
            </div>
        `;
        tryRenderEditButton(tile, body);
        loadSimilarTiles(tile.name, body);
    }
}

// Refresh active lightbox window helper
window.refreshActiveLightbox = function() {
    if (activeLightboxTile) {
        if (!activeLightboxTile.content_file) {
            fetch(`api.php?action=get_tile&name=${encodeURIComponent(activeLightboxTile.name)}&lang=${activeLightboxTile.language}`)
                .then(r => r.json())
                .then(r => {
                    if (r.status === 'success') {
                        openLightbox(r.data, false);
                    }
                });
        } else {
            openLightbox(activeLightboxTile, false);
        }
    }
};


// Fetch and append similar tiles at the bottom of the lightbox
function loadSimilarTiles(tileName, targetBody) {
    fetch(`api.php?action=similar&name=${encodeURIComponent(tileName)}&lang=${lang}&limit=3`)
        .then(res => res.json())
        .then(res => {
            if (res.status === 'success' && res.data && res.data.length > 0) {
                const section = document.createElement('div');
                section.className = 'see-also-section';
                const titleText = (lang === 'en') ? 'Related Topics' : 'Ähnliche Themen';
                section.innerHTML = `<h3 class="see-also-title">${titleText}</h3>`;
                
                const grid = document.createElement('div');
                grid.className = 'see-also-grid';
                
                res.data.forEach(item => {
                    // Create the tile element using the same builder function as the main page
                    const card = createTileElement(item, true);
                    
                    // Clone the element to remove standard click listeners, then apply modal transition logic
                    const cleanCard = card.cloneNode(true);
                    cleanCard.style.setProperty('--tile-color', item.accent_color || '#fbbf24');
                    
                    cleanCard.addEventListener('click', () => {
                        if (item.type === 'link') {
                            if (item.link) window.open(item.link, '_blank');
                        } else {
                            // Replace currently open lightbox tile and update URL history state
                            openLightbox(item, true);
                        }
                    });
                    grid.appendChild(cleanCard);
                });
                
                section.appendChild(grid);
                targetBody.appendChild(section);
            }
        })
        .catch(err => console.error("Error loading similar tiles:", err));
}

// Setup Scroll Observer for infinite scroll
function setupScrollObserver() {
    if (observer) observer.disconnect();
    
    const trigger = document.getElementById('loadingTrigger');
    observer = new IntersectionObserver((entries) => {
        if (entries[0].isIntersecting && hasMore && !loading) {
            loadMore();
        }
    }, {
        rootMargin: '100px'
    });
    
    observer.observe(trigger);
}

// Debounce helper
function debounce(func, wait) {
    let timeout;
    return function(...args) {
        clearTimeout(timeout);
        timeout = setTimeout(() => func.apply(this, args), wait);
    };
}

// Setup click and input listeners
function setupEventHandlers() {
    // Debounced search
    const searchInput = document.getElementById('searchInput');
    searchInput.addEventListener('input', debounce((e) => {
        q = e.target.value.trim();
        resetAndLoad();
    }, 400));
    
    // Clear suggestion activity if typing
    searchInput.addEventListener('keydown', () => {
        const container = document.getElementById('categorySuggestions');
        if (container) {
            container.querySelectorAll('.suggestion-pill').forEach(p => p.classList.remove('active'));
        }
    });

    // Infinite Scroll setup
    setupScrollObserver();

    // Dialog closers (only non-admin ones)
    const detailsDialogEl = document.getElementById('detailsDialog');
    if (detailsDialogEl) {
        document.getElementById('closeDetailsBtn').addEventListener('click', () => {
            detailsDialogEl.close();
        });

        // Reset URL hashtag when dialog is closed cleanly without triggering event loop
        detailsDialogEl.addEventListener('close', () => {
            activeLightboxTile = null;
            if (window.location.hash) {
                isProgrammaticRouting = true;
                const cleanUrl = window.location.pathname + window.location.search;
                history.pushState(null, '', cleanUrl);
                setTimeout(() => { isProgrammaticRouting = false; }, 0);
            }
        });
    }

    // Back-navigation history event listeners
    const handleNavEvent = () => {
        if (!isProgrammaticRouting) {
            handleURLRouting();
        }
    };
    window.addEventListener('popstate', handleNavEvent);
    window.addEventListener('hashchange', handleNavEvent);

    // Close on click outside modal
    window.addEventListener('click', (e) => {
        const details = document.getElementById('detailsDialog');
        const login = document.getElementById('loginDialog');
        if (e.target === details) details.close();
        if (login && e.target === login) login.close();
        
        // If admin script is loaded and active, delegate modal clicking outside to close
        if (window.adminScriptLoaded && typeof window.handleAdminModalClose === 'function') {
            window.handleAdminModalClose(e);
        }
    });

    // Admin trigger action (loads admin.js on demand!)
    document.getElementById('adminTrigger').addEventListener('click', () => {
        if (!isAdmin) {
            lazyLoadAdmin(() => {
                document.getElementById('loginDialog').showModal();
                document.getElementById('adminPassword').focus();
            });
        }
    });

    // Theme Toggle Button click handler
    const themeBtn = document.getElementById('themeToggleBtn');
    if (themeBtn) {
        themeBtn.addEventListener('click', () => {
            const isLight = document.body.classList.contains('light-theme');
            if (isLight) {
                document.body.classList.remove('light-theme');
                localStorage.setItem('theme', 'dark');
                themeBtn.innerHTML = '<i class="fa-solid fa-moon"></i>';
            } else {
                document.body.classList.add('light-theme');
                localStorage.setItem('theme', 'light');
                themeBtn.innerHTML = '<i class="fa-solid fa-sun"></i>';
            }
            
            // Refresh active Monaco editor themes if open
            if (window.monacoLoaded && window.monaco) {
                const monacoTheme = document.body.classList.contains('light-theme') ? 'vs' : 'vs-dark';
                monaco.editor.setTheme(monacoTheme);
            }
        });
    }

    // Language dropdown handlers
    const langDropdown = document.getElementById('langDropdown');
    const langTriggerBtn = document.getElementById('langTriggerBtn');
    if (langTriggerBtn && langDropdown) {
        langTriggerBtn.addEventListener('click', (e) => {
            e.preventDefault();
            e.stopPropagation();
            langDropdown.classList.toggle('active');
        });
        
        // Close dropdown when clicking outside
        document.addEventListener('click', () => {
            langDropdown.classList.remove('active');
        });
    }
    
    // Language items switch handlers (using event delegation on the dropdown menu)
    const langDropdownMenu = document.getElementById('langDropdownMenu');
    if (langDropdownMenu) {
        langDropdownMenu.addEventListener('click', (e) => {
            const item = e.target.closest('.dropdown-item');
            if (!item) return;
            
            e.preventDefault();
            const selectedLang = item.getAttribute('data-lang');
            if (selectedLang && selectedLang !== lang) {
                lang = selectedLang;
                localStorage.setItem('lang', lang);
                
                // Update UI, reload configs and tiles
                updateLanguageUI();
                applyHeroConfig();
                applySuggestionsConfig();
                resetAndLoad();
            }
            if (langDropdown) {
                langDropdown.classList.remove('active');
            }
        });
    }
}

// -------------------------------------------------------------
// ADMINISTRATIVE LOADERS
// -------------------------------------------------------------

// Verify admin session on load
function checkAdminStatus() {
    fetch('admin.php?action=status')
        .then(res => res.json())
        .then(res => {
            if (res.status === 'success' && res.logged_in) {
                lazyLoadAdmin(() => {
                    setAdminMode(true);
                    resetAndLoad(); // Reload list to include invisible tiles
                });
            }
        })
        .catch(err => console.error("Error checking auth status:", err));
}

// Lazy-load admin operations script and styles
function lazyLoadAdmin(callback) {
    if (adminScriptLoaded) {
        if (callback) callback();
        return;
    }
    
    // 1. Load admin.css stylesheet
    const link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = 'admin.css?v=' + Date.now();
    document.head.appendChild(link);

    // 2. Load admin.html (injecting dialogs) and admin.js in parallel
    const htmlPromise = fetch('admin.html?v=' + Date.now())
        .then(response => {
            if (!response.ok) throw new Error("Could not load admin.html");
            return response.text();
        });

    const scriptPromise = new Promise((resolve, reject) => {
        const script = document.createElement('script');
        script.src = 'admin.js?v=' + Date.now();
        script.onload = () => resolve();
        script.onerror = () => reject(new Error("Error loading admin.js"));
        document.head.appendChild(script);
    });

    Promise.all([htmlPromise, scriptPromise])
        .then(([html]) => {
            // Inject the dialog elements into the DOM
            const tempDiv = document.createElement('div');
            tempDiv.innerHTML = html;
            
            const dialogs = tempDiv.querySelectorAll('dialog');
            dialogs.forEach(dialog => {
                document.body.appendChild(dialog);
            });

            // Inject Admin Bar into .container if loaded from admin.html
            const adminBar = tempDiv.querySelector('#adminBar');
            if (adminBar && !document.getElementById('adminBar')) {
                const container = document.querySelector('.container');
                if (container) {
                    container.insertBefore(adminBar, container.firstChild);
                } else {
                    document.body.insertBefore(adminBar, document.body.firstChild);
                }
            }

            // Initialize admin listeners and scripts
            if (typeof window.initAdmin === 'function') {
                window.initAdmin();
            } else {
                throw new Error("initAdmin not found on window object");
            }

            adminScriptLoaded = true;
            window.adminScriptLoaded = true;
            if (callback) callback();
        })
        .catch(err => {
            console.error("Error lazy-loading admin resources in parallel:", err);
        });
}

// Enable/Disable admin view controls
function setAdminMode(active) {
    isAdmin = active;
    const adminBar = document.getElementById('adminBar');
    if (adminBar) {
        if (active) {
            adminBar.classList.add('active');
            lazyLoadMonaco();
        } else {
            adminBar.classList.remove('active');
        }
    }
}
