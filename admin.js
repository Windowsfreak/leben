// -------------------------------------------------------------
// ADMINISTRATIVE PANEL & CONTROLS (admin.js)
// -------------------------------------------------------------

// Local Admin Editor State
let monacoHtmlEditorInstance = null;
let monacoSettingsEditorInstance = null;
let monacoLightboxEditorInstance = null;
let contentFileWasEmpty = false;
let currentActiveEditor = null; // 'tile' or 'lightbox'
let monacoLoaded = false;
let formDirty = false;
let isPopulating = false;

// Expose states to app.js for unload warning listeners
window.isEditing = false;
window.isSettingsEditing = false;
window.monacoLoaded = false;

// Perform login request
function login(password) {
    const formData = new FormData();
    formData.append('password', password);
    
    fetch('admin.php?action=login', {
        method: 'POST',
        body: formData
    })
        .then(res => {
            if (!res.ok) throw new Error("Invalid password");
            return res.json();
        })
        .then(res => {
            if (res.status === 'success') {
                setAdminMode(true);
                document.getElementById('loginDialog').close();
                document.getElementById('adminPassword').value = '';
                resetAndLoad(); // Reload list to include invisible tiles
            }
        })
        .catch(err => {
            alert("Passwort inkorrekt.");
            console.error("Login failed:", err);
        });
}

// Perform logout request
function logout() {
    fetch('admin.php?action=logout')
        .then(res => res.json())
        .then(res => {
            if (res.status === 'success') {
                setAdminMode(false);
                resetAndLoad(); // Reload list to hide invisible tiles
            }
        })
        .catch(err => console.error("Logout failed:", err));
}

// Enable/Disable admin view controls
function setAdminMode(active) {
    isAdmin = active;
    const adminBar = document.getElementById('adminBar');
    if (adminBar) {
        if (active) {
            adminBar.classList.add('active');
            lazyLoadMonaco();
            fetchTranslationStatuses();
        } else {
            adminBar.classList.remove('active');
        }
    }
}

// Open Editor Modal (creates a new tile if no tile parameters passed)
function openEditor(tile = null) {
    window.isEditing = true;
    formDirty = false;
    isPopulating = true;
    
    // Reset tab state to default preview mode
    document.getElementById('tabPreviewBtn').classList.add('active');
    document.getElementById('tabCodeBtn').classList.remove('active');
    document.getElementById('editorPreviewContainer').classList.add('active');
    document.getElementById('editorCodeContainer').classList.remove('active');
    
    const dialog = document.getElementById('editorDialog');
    document.getElementById('editorForm').reset();
    
    let htmlContent = '';
    
    if (tile) {
        document.getElementById('editorDialogTitle').textContent = `Kachel bearbeiten: ${tile.title}`;
        document.getElementById('editId').value = tile.id;
        document.getElementById('editName').value = tile.name;
        document.getElementById('editLanguage').value = tile.language;
        document.getElementById('editTitle').value = tile.title;
        
        // Clean Postgres array tag format '{tag1,tag2}' to 'tag1, tag2'
        const rawTags = tile.tags || '';
        const tagsClean = rawTags.replace(/[\{\}]/g, '');
        document.getElementById('editCategoryTags').value = tagsClean;
        
        document.getElementById('editSummary').value = tile.summary || '';
        document.getElementById('editReferenceType').value = tile.type || 'doc';
        document.getElementById('editLink').value = tile.link || '';
        document.getElementById('editContentFile').value = tile.content_file || '';
        document.getElementById('editHtmlContent').value = tile.html_teaser || '';
        document.getElementById('editVisible').checked = tile.visible;
        document.getElementById('editSortOrder').value = tile.sort_order || 100;
        document.getElementById('editBackground').value = tile.background || '';
        
        // Populate color inputs
        const color = tile.accent_color || '#fbbf24';
        document.getElementById('editAccentColor').value = color;
        document.getElementById('editAccentColorPicker').value = color;
        
        htmlContent = tile.html_teaser || '';
     } else {
        document.getElementById('editorDialogTitle').textContent = "Neue Kachel erstellen";
        document.getElementById('editId').value = '';
        document.getElementById('editLanguage').value = lang; // default to active browser language
        document.getElementById('editSortOrder').value = '100';
        document.getElementById('editAccentColor').value = '#fbbf24';
        document.getElementById('editAccentColorPicker').value = '#fbbf24';
        document.getElementById('editBackground').value = '';
        
        htmlContent = '<h3>Neue Kachel</h3>\n<p>HTML-Struktur hier einfügen...</p>';
        document.getElementById('editHtmlContent').value = htmlContent;
     }

    const saveAndSyncBtn = document.getElementById('saveAndSyncBtn');
    if (saveAndSyncBtn) {
        saveAndSyncBtn.style.display = (tile && tile.id) ? 'inline-flex' : 'none';
    }
    
    // Toggle correct input fields depending on reference type
    const refType = document.getElementById('editReferenceType').value;
    toggleReferenceFields(refType);
    
    contentFileWasEmpty = !document.getElementById('editContentFile').value;
    
    // Load Monaco HTML editor instance
    lazyLoadMonaco(() => {
        isPopulating = true;
        const container = document.getElementById('monacoHtmlEditor');
        const fallbackTextarea = document.getElementById('editHtmlContent');
        
        fallbackTextarea.style.display = 'none';
        container.style.display = 'block';
        
        if (!monacoHtmlEditorInstance) {
            monacoHtmlEditorInstance = monaco.editor.create(container, {
                value: htmlContent,
                language: 'html',
                theme: document.body.classList.contains('light-theme') ? 'vs' : 'vs-dark',
                minimap: { enabled: false },
                fontSize: 12,
                automaticLayout: true
            });
            
            // Set dirty state on change
            monacoHtmlEditorInstance.onDidChangeModelContent(() => {
                fallbackTextarea.value = monacoHtmlEditorInstance.getValue();
                if (!isPopulating) {
                    formDirty = true;
                }
                updateLivePreview();
            });
        } else {
            monacoHtmlEditorInstance.setValue(htmlContent);
            monaco.editor.setTheme(document.body.classList.contains('light-theme') ? 'vs' : 'vs-dark');
        }
        
        // Reset initialization states after loading finishes
        isPopulating = false;
        formDirty = false;
        updateLivePreview();
    });
    
    dialog.showModal();
}

// Toggle input fields in editor dialog based on clicked target behavior
function toggleReferenceFields(type) {
    const linkFields = document.getElementById('editorLinkFields');
    const lightboxFields = document.getElementById('editorLightboxFields');
    if (type === 'link') {
        linkFields.style.display = 'block';
        lightboxFields.style.display = 'none';
    } else {
        linkFields.style.display = 'none';
        lightboxFields.style.display = 'block';
    }
}

// Update card layout preview dynamically based on active settings in editor modal
function updateLivePreview() {
    const title = document.getElementById('editTitle').value || 'Titel-Vorschau';
    const color = document.getElementById('editAccentColor').value || '#fbbf24';
    const background = document.getElementById('editBackground').value || '';
    
    let html = '';
    if (monacoHtmlEditorInstance && monacoLoaded) {
        html = monacoHtmlEditorInstance.getValue();
    } else {
        html = document.getElementById('editHtmlContent').value;
    }
    
    const previewTile = document.getElementById('tileLivePreview');
    const previewContent = document.getElementById('tileLivePreviewContent');
    
    if (previewTile && previewContent) {
        previewTile.style.setProperty('--tile-color', color);
        if (typeof window.applyTileTheme === 'function') {
            window.applyTileTheme(previewTile, background);
        } else if (background.trim() !== '') {
            previewTile.style.background = background.trim();
        } else {
            previewTile.style.background = '';
        }
        previewContent.innerHTML = html;
    }
}

// Automatically detect background brightness, saturation, and primary color from background input
function detectBackgroundColorAndTheme() {
    const input = document.getElementById('editBackground');
    if (!input) return;
    
    let bgStr = input.value || '';
    if (!bgStr.trim()) {
        alert("Bitte zuerst ein Hintergrundbild oder eine Farbe in das Feld eingeben.");
        return;
    }

    // Clean out existing comment tag
    const cleanBg = bgStr.replace(/\/\*.*?\*\//g, '').trim();

    // Check if it contains an image URL
    const urlMatch = cleanBg.match(/url\(['"]?(.*?)['"]?\)/i);
    let imageUrl = urlMatch ? urlMatch[1] : null;

    // Helper to evaluate average RGB to HSL and decide theme tag
    const processRgb = (r, g, b) => {
        const rNorm = r / 255;
        const gNorm = g / 255;
        const bNorm = b / 255;
        const max = Math.max(rNorm, gNorm, bNorm);
        const min = Math.min(rNorm, gNorm, bNorm);
        const d = max - min;

        let h = 0;
        let s = 0;
        const l = (max + min) / 2;

        if (d !== 0) {
            s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
            switch (max) {
                case rNorm: h = (gNorm - bNorm) / d + (gNorm < bNorm ? 6 : 0); break;
                case gNorm: h = (bNorm - rNorm) / d + 2; break;
                case bNorm: h = (rNorm - gNorm) / d + 4; break;
            }
            h *= 60;
        }

        const lPct = l * 100;
        const sPct = s * 100;

        let detectedTag = '';

        // Rule: If brightness is between 25% and 75% AND saturation > 30%, determine closest primary color
        if (lPct >= 25 && lPct <= 75 && sPct > 30) {
            let primaryColor = '';
            if (h >= 340 || h < 25) {
                primaryColor = 'red';
            } else if (h >= 25 && h < 80) {
                primaryColor = 'yellow';
            } else if (h >= 80 && h < 165) {
                primaryColor = 'green';
            } else if (h >= 165 && h < 260) {
                primaryColor = 'blue';
            } else if (h >= 260 && h < 340) {
                primaryColor = 'red';
            }

            if (lPct < 40) {
                detectedTag = (primaryColor === 'blue' || primaryColor === 'red') ? primaryColor : `dark${primaryColor}`;
            } else if (lPct > 60) {
                detectedTag = `light${primaryColor}`;
            } else {
                detectedTag = primaryColor;
            }
        } else {
            // Otherwise: dark if brightness < 40%, light if brightness >= 40%
            if (lPct < 40) {
                detectedTag = 'dark';
            } else {
                detectedTag = 'light';
            }
        }

        input.value = `${cleanBg} /*${detectedTag}*/`;
        formDirty = true;
        updateLivePreview();
    };

    if (imageUrl) {
        const img = new Image();
        img.crossOrigin = 'Anonymous';
        img.onload = () => {
            try {
                const canvas = document.createElement('canvas');
                canvas.width = 50;
                canvas.height = 50;
                const ctx = canvas.getContext('2d');
                ctx.drawImage(img, 0, 0, 50, 50);
                const imgData = ctx.getImageData(0, 0, 50, 50).data;

                let rSum = 0, gSum = 0, bSum = 0, count = 0;
                for (let i = 0; i < imgData.length; i += 4) {
                    const alpha = imgData[i + 3];
                    if (alpha > 128) {
                        rSum += imgData[i];
                        gSum += imgData[i + 1];
                        bSum += imgData[i + 2];
                        count++;
                    }
                }
                if (count > 0) {
                    processRgb(rSum / count, gSum / count, bSum / count);
                } else {
                    processRgb(128, 128, 128);
                }
            } catch (err) {
                console.warn("Canvas image reading restricted, falling back to default.", err);
                input.value = `${cleanBg} /*dark*/`;
                formDirty = true;
                updateLivePreview();
            }
        };
        img.onerror = () => {
            alert("Hintergrundbild konnte nicht geladen werden.");
        };
        img.src = imageUrl;
    } else {
        const tempElem = document.createElement('div');
        tempElem.style.color = cleanBg;
        document.body.appendChild(tempElem);
        const computedColor = getComputedStyle(tempElem).color;
        document.body.removeChild(tempElem);

        const rgbMatch = computedColor.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
        if (rgbMatch) {
            processRgb(parseInt(rgbMatch[1]), parseInt(rgbMatch[2]), parseInt(rgbMatch[3]));
        } else {
            alert("Konnte die Farbe aus der Eingabe nicht auslesen.");
        }
    }
}

// Safe editor closer checks if inputs are dirty
function closeEditorWithConfirm() {
    if (formDirty) {
        if (!confirm("Möchtest du die ungespeicherten Änderungen wirklich verwerfen?")) {
            return;
        }
    }
    document.getElementById('editorDialog').close();
    window.isEditing = false;
    formDirty = false;
}

// Open global settings editor dialog
function openSettingsEditor() {
    window.isSettingsEditing = true;
    const dialog = document.getElementById('settingsDialog');
    
    if (!appConfig) {
        alert("Konfigurationsdatei wurde noch nicht geladen.");
        return;
    }
    
    // Dynamically convert JSON configuration state to YAML for Monaco
    const settingsYaml = jsyaml.dump(appConfig);
    document.getElementById('editSettingsJson').value = settingsYaml;
    
    lazyLoadMonaco(() => {
        const container = document.getElementById('monacoSettingsEditor');
        const fallbackTextarea = document.getElementById('editSettingsJson');
        
        fallbackTextarea.style.display = 'none';
        container.style.display = 'block';
        
        if (!monacoSettingsEditorInstance) {
            monacoSettingsEditorInstance = monaco.editor.create(container, {
                value: settingsYaml,
                language: 'yaml', // Set language to YAML
                theme: document.body.classList.contains('light-theme') ? 'vs' : 'vs-dark',
                minimap: { enabled: false },
                fontSize: 12,
                automaticLayout: true
            });
        } else {
            monacoSettingsEditorInstance.setValue(settingsYaml);
            monaco.editor.setTheme(document.body.classList.contains('light-theme') ? 'vs' : 'vs-dark');
            const model = monacoSettingsEditorInstance.getModel();
            if (model) {
                monaco.editor.setModelLanguage(model, 'yaml');
            }
        }
    });
    
    dialog.showModal();
}

// Save global settings (converts YAML back to validated JSON)
function saveSettings() {
    let settingsValue = '';
    if (monacoSettingsEditorInstance && monacoLoaded) {
        settingsValue = monacoSettingsEditorInstance.getValue();
    } else {
        settingsValue = document.getElementById('editSettingsJson').value;
    }
    
    let parsedConfig = null;
    
    // Parse YAML client-side
    try {
        parsedConfig = jsyaml.load(settingsValue);
    } catch (e) {
        alert("YAML Syntaxfehler: " + e.message);
        return;
    }
    
    // Validate configuration structure & data types
    try {
        validateConfig(parsedConfig);
    } catch (e) {
        alert("Konfigurationsfehler: " + e.message);
        return;
    }
    
    const saveBtn = document.getElementById('saveSettingsBtn');
    const origHtml = saveBtn.innerHTML;
    saveBtn.disabled = true;
    saveBtn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Speichere...';
    
    const jsonString = JSON.stringify(parsedConfig, null, 2);
    
    const formData = new FormData();
    formData.append('config_json', jsonString);
    
    fetch('admin.php?action=save_config', {
        method: 'POST',
        body: formData
    })
    .then(res => res.json())
    .then(res => {
        if (res.status === 'success') {
            alert(res.message);
            document.getElementById('settingsDialog').close();
            window.isSettingsEditing = false;
            
            // Reload configuration and refresh UI
            loadConfig(() => {
                resetAndLoad();
            });
        } else {
            alert(`Speichern fehlgeschlagen: ${res.message}`);
        }
    })
    .catch(err => {
        alert("Verbindungsfehler beim Speichern der Einstellungen.");
        console.error(err);
    })
    .finally(() => {
        saveBtn.disabled = false;
        saveBtn.innerHTML = origHtml;
    });
}

// Close settings confirm dialogue
function closeSettingsWithConfirm() {
    document.getElementById('settingsDialog').close();
    window.isSettingsEditing = false;
}

// Save (Insert / Update) tile details, optionally syncing non-language settings to sibling tiles
function saveTile(syncSiblings = false) {
    const id = document.getElementById('editId').value;
    const form = document.getElementById('editorForm');
    
    const htmlContent = (monacoHtmlEditorInstance && monacoLoaded) 
        ? monacoHtmlEditorInstance.getValue() 
        : document.getElementById('editHtmlContent').value;
        
    const formData = new FormData();
    if (id) formData.append('id', id);
    formData.append('name', document.getElementById('editName').value);
    formData.append('language', document.getElementById('editLanguage').value);
    formData.append('title', document.getElementById('editTitle').value);
    formData.append('tags', document.getElementById('editCategoryTags').value);
    formData.append('summary', document.getElementById('editSummary').value);
    formData.append('type', document.getElementById('editReferenceType').value);
    formData.append('link', document.getElementById('editLink').value);
    formData.append('content_file', document.getElementById('editContentFile').value);
    formData.append('html_teaser', htmlContent);
    formData.append('visible', document.getElementById('editVisible').checked ? 'true' : 'false');
    formData.append('sort_order', document.getElementById('editSortOrder').value);
    formData.append('accent_color', document.getElementById('editAccentColor').value);
    formData.append('background', document.getElementById('editBackground').value);
    if (syncSiblings) {
        formData.append('sync_siblings', 'true');
    }
    
    // Show spinner in active save button during network request
    const saveBtn = document.getElementById('saveTileBtn');
    const syncBtn = document.getElementById('saveAndSyncBtn');
    const targetBtn = (syncSiblings && syncBtn) ? syncBtn : saveBtn;
    const origHtml = targetBtn.innerHTML;
    
    saveBtn.disabled = true;
    if (syncBtn) syncBtn.disabled = true;
    targetBtn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Speichere...';
    
    fetch('admin.php?action=save', {
        method: 'POST',
        body: formData
    })
        .then(res => {
            if (!res.ok) throw new Error("Save error");
            return res.json();
        })
        .then(res => {
            if (res.status === 'success') {
                window.isEditing = false;
                formDirty = false;
                document.getElementById('editorDialog').close();
                resetAndLoad();
            } else {
                alert(`Fehler beim Speichern: ${res.message}`);
            }
        })
        .catch(err => {
            alert("Fehler beim Kommunizieren mit dem Server.");
            console.error("Save error:", err);
        })
        .finally(() => {
            saveBtn.disabled = false;
            if (syncBtn) syncBtn.disabled = false;
            targetBtn.innerHTML = origHtml;
        });
}

// Quick action: Toggle visibility flag of a tile
function toggleVisibility(id) {
    const formData = new FormData();
    formData.append('id', id);
    
    fetch('admin.php?action=toggle_visibility', {
        method: 'POST',
        body: formData
    })
        .then(res => res.json())
        .then(res => {
            if (res.status === 'success') {
                resetAndLoad();
            }
        })
        .catch(err => console.error("Visibility toggle error:", err));
}

// Quick action: Clone an existing tile
function cloneTile(id) {
    const formData = new FormData();
    formData.append('id', id);
    
    fetch('admin.php?action=clone', {
        method: 'POST',
        body: formData
    })
        .then(res => res.json())
        .then(res => {
            if (res.status === 'success') {
                resetAndLoad();
            } else {
                alert(`Klonen fehlgeschlagen: ${res.message}`);
            }
        })
        .catch(err => console.error("Cloning error:", err));
}

// Quick action: Delete tile
function deleteTile(id, title) {
    if (!confirm(`Soll die Kachel "${title}" wirklich unwiderruflich gelöscht werden?`)) {
        return;
    }
    
    const formData = new FormData();
    formData.append('id', id);
    
    fetch('admin.php?action=delete', {
        method: 'POST',
        body: formData
    })
        .then(res => res.json())
        .then(res => {
            if (res.status === 'success') {
                resetAndLoad();
            }
        })
        .catch(err => console.error("Delete error:", err));
}

// Client-side schema validation for config properties & structures
function validateConfig(config) {
    if (!config || typeof config !== 'object') {
        throw new Error("Konfiguration muss ein valides Objekt sein.");
    }
    
    // 1. Check supported_languages
    if (!config.supported_languages || typeof config.supported_languages !== 'object' || Array.isArray(config.supported_languages)) {
        throw new Error("'supported_languages' muss ein Objekt sein.");
    }
    
    const supportedCodes = Object.keys(config.supported_languages);
    if (supportedCodes.length === 0) {
        throw new Error("Mindestens eine unterstützte Sprache muss konfiguriert sein.");
    }
    
    Object.entries(config.supported_languages).forEach(([code, name]) => {
        if (typeof code !== 'string' || typeof name !== 'string') {
            throw new Error("Sprachcodes und Anzeigenamen in 'supported_languages' müssen Texte sein.");
        }
    });

    // 2. Check hero per language
    if (!config.hero || typeof config.hero !== 'object') {
        throw new Error("'hero' muss ein Objekt sein.");
    }
    supportedCodes.forEach(lang => {
        if (!config.hero[lang] || typeof config.hero[lang] !== 'object') {
            throw new Error(`Hero-Texte fehlen für die konfigurierte Sprache '${lang}'.`);
        }
        if (typeof config.hero[lang].title !== 'string') {
            throw new Error(`'hero.${lang}.title' muss ein Text sein.`);
        }
        if (typeof config.hero[lang].tagline !== 'string') {
            throw new Error(`'hero.${lang}.tagline' muss ein Text sein.`);
        }
        if (config.hero[lang].searchPlaceholder && typeof config.hero[lang].searchPlaceholder !== 'string') {
            throw new Error(`'hero.${lang}.searchPlaceholder' muss ein Text sein.`);
        }
    });

    // 3. Check wallpapers list
    if (!Array.isArray(config.wallpapers)) {
        throw new Error("'wallpapers' muss eine Liste (Array) sein.");
    }
    config.wallpapers.forEach(wp => {
        if (typeof wp !== 'string') {
            throw new Error("Dateinamen in 'wallpapers' müssen Texte sein.");
        }
    });

    // 4. Check search suggestions per language
    if (!config.suggestions || typeof config.suggestions !== 'object') {
        throw new Error("'suggestions' muss ein Objekt sein.");
    }
    supportedCodes.forEach(lang => {
        if (!config.suggestions[lang] || typeof config.suggestions[lang] !== 'object') {
            throw new Error(`Suchvorschläge fehlen für die konfigurierte Sprache '${lang}'.`);
        }
        Object.entries(config.suggestions[lang]).forEach(([key, val]) => {
            if (typeof key !== 'string' || typeof val !== 'string') {
                throw new Error(`Einträge in 'suggestions.${lang}' müssen Text-Schlüssel und Text-Werte sein.`);
            }
        });
    });
}

// Lazy-load Monaco Code Editor and JS-YAML parser from CDN
function lazyLoadMonaco(callback) {
    if (monacoLoaded) {
        if (callback) callback();
        return;
    }
    
    // Inject JS-YAML loader script first
    const yamlScript = document.createElement('script');
    yamlScript.src = 'https://cdnjs.cloudflare.com/ajax/libs/js-yaml/4.1.0/js-yaml.min.js';
    yamlScript.onload = () => {
        const loaderScript = document.createElement('script');
        loaderScript.src = 'https://cdnjs.cloudflare.com/ajax/libs/monaco-editor/0.39.0/min/vs/loader.min.js';
        loaderScript.onload = () => {
            require.config({ paths: { vs: 'https://cdnjs.cloudflare.com/ajax/libs/monaco-editor/0.39.0/min/vs' } });
            require(['vs/editor/editor.main'], () => {
                monacoLoaded = true;
                window.monacoLoaded = true;
                if (callback) callback();
            });
        };
        document.head.appendChild(loaderScript);
    };
    document.head.appendChild(yamlScript);
}

// -------------------------------------------------------------
// BIND ALL ADMINISTRATIVE EVENTS
// -------------------------------------------------------------
function initAdmin() {
    // Register all admin-related event listeners
    document.getElementById('closeLoginBtn').addEventListener('click', () => {
        document.getElementById('loginDialog').close();
    });
    
    document.getElementById('closeEditorBtn').addEventListener('click', closeEditorWithConfirm);
    document.getElementById('cancelEditorBtn').addEventListener('click', closeEditorWithConfirm);
    document.getElementById('autoTranslateBtn').addEventListener('click', triggerAutoTranslate);
    
    const detectColorBtn = document.getElementById('detectThemeColorBtn');
    if (detectColorBtn) {
        detectColorBtn.addEventListener('click', detectBackgroundColorAndTheme);
    }
    
    document.getElementById('closeSettingsBtn').addEventListener('click', closeSettingsWithConfirm);
    document.getElementById('cancelSettingsBtn').addEventListener('click', closeSettingsWithConfirm);
    
    // Login Form Submit
    document.getElementById('loginForm').addEventListener('submit', (e) => {
        e.preventDefault();
        const pass = document.getElementById('adminPassword').value;
        login(pass);
    });
    
    // Logout Button
    document.getElementById('logoutBtn').addEventListener('click', logout);

    // Add Tile Button
    document.getElementById('addTileBtn').addEventListener('click', () => {
        openEditor();
    });

    // Settings Button
    document.getElementById('editSettingsBtn').addEventListener('click', openSettingsEditor);

    // Save Settings Button
    document.getElementById('saveSettingsBtn').addEventListener('click', saveSettings);

    // Refresh Vectors Button
    document.getElementById('refreshVectorsBtn').addEventListener('click', () => {
        if (!confirm("Sollen wirklich alle Vektoren in der Datenbank neu generiert werden? Das kann einige Sekunden dauern.")) {
            return;
        }
        const btn = document.getElementById('refreshVectorsBtn');
        const origHtml = btn.innerHTML;
        btn.disabled = true;
        btn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Generiere...';

        fetch('admin.php?action=refresh_vectors', { method: 'POST' })
            .then(res => res.json())
            .then(res => {
                if (res.status === 'success') {
                    alert(res.message);
                    resetAndLoad();
                } else {
                    alert(`Fehler beim Aktualisieren: ${res.message}`);
                }
            })
            .catch(err => {
                alert("Fehler beim Kommunizieren mit dem Server.");
                console.error(err);
            })
            .finally(() => {
                btn.disabled = false;
                btn.innerHTML = origHtml;
            });
    });

    // Editor Preview/Code Tab switching
    document.getElementById('tabPreviewBtn').addEventListener('click', () => {
        document.getElementById('tabPreviewBtn').classList.add('active');
        document.getElementById('tabCodeBtn').classList.remove('active');
        document.getElementById('editorPreviewContainer').classList.add('active');
        document.getElementById('editorCodeContainer').classList.remove('active');
        updateLivePreview();
    });
    
    document.getElementById('tabCodeBtn').addEventListener('click', () => {
        document.getElementById('tabCodeBtn').classList.add('active');
        document.getElementById('tabPreviewBtn').classList.remove('active');
        document.getElementById('editorCodeContainer').classList.add('active');
        document.getElementById('editorPreviewContainer').classList.remove('active');
        
        setTimeout(() => {
            if (monacoHtmlEditorInstance) {
                monacoHtmlEditorInstance.layout();
                monacoHtmlEditorInstance.focus();
            }
        }, 50);
    });

    // Form inputs modify listeners to track dirtiness and update previews
    const formInputs = ['editName', 'editLanguage', 'editTitle', 'editCategoryTags', 'editSummary', 'editLink', 'editContentFile', 'editAccentColor', 'editVisible', 'editSortOrder', 'editBackground'];
    formInputs.forEach(id => {
        const el = document.getElementById(id);
        if (el) {
            el.addEventListener('input', () => {
                formDirty = true;
                updateLivePreview();
                if ((id === 'editName' || id === 'editLanguage') && contentFileWasEmpty) {
                    const refType = document.getElementById('editReferenceType').value;
                    if (refType === 'lightbox') {
                        const name = document.getElementById('editName').value.trim();
                        const langCode = document.getElementById('editLanguage').value.trim();
                        if (name && langCode) {
                            document.getElementById('editContentFile').value = `${name}-${langCode}.html`;
                        }
                    }
                }
                if (id === 'editContentFile') {
                    if (el.value !== '') {
                        contentFileWasEmpty = false;
                    }
                }
            });
            el.addEventListener('change', () => {
                formDirty = true;
                updateLivePreview();
            });
        }
    });

    // Hook color picker to hex text field and update live preview
    const picker = document.getElementById('editAccentColorPicker');
    if (picker) {
        picker.addEventListener('input', (e) => {
            document.getElementById('editAccentColor').value = e.target.value;
            formDirty = true;
            updateLivePreview();
        });
    }

    // Toggle fields based on Reference Type dropdown selection
    const refType = document.getElementById('editReferenceType');
    if (refType) {
        refType.addEventListener('change', (e) => {
            formDirty = true;
            toggleReferenceFields(e.target.value);
            if (e.target.value === 'lightbox' && contentFileWasEmpty) {
                const name = document.getElementById('editName').value.trim();
                const langCode = document.getElementById('editLanguage').value.trim();
                if (name && langCode) {
                    document.getElementById('editContentFile').value = `${name}-${langCode}.html`;
                }
            }
        });
    }

    // Save Button Form Submit
    document.getElementById('editorForm').addEventListener('submit', (e) => {
        e.preventDefault();
        saveTile();
    });

    const saveAndSyncBtn = document.getElementById('saveAndSyncBtn');
    if (saveAndSyncBtn) {
        saveAndSyncBtn.addEventListener('click', (e) => {
            e.preventDefault();
            saveTile(true);
        });
    }

    // Lightbox Editor Form Submit
    document.getElementById('closeLightboxEditorBtn').addEventListener('click', () => {
        document.getElementById('lightboxEditorDialog').close();
        window.isEditing = false;
    });
    document.getElementById('cancelLightboxEditorBtn').addEventListener('click', () => {
        document.getElementById('lightboxEditorDialog').close();
        window.isEditing = false;
    });
    document.getElementById('lightboxEditorForm').addEventListener('submit', (e) => {
        e.preventDefault();
        saveLightboxEditor();
    });

    // LLM prompts modal triggers
    document.getElementById('tileLLMHelperBtn').addEventListener('click', () => {
        currentActiveEditor = 'tile';
        document.getElementById('llmPromptInput').value = '';
        document.getElementById('llmPromptDialog').showModal();
    });
    document.getElementById('lightboxLLMHelperBtn').addEventListener('click', () => {
        currentActiveEditor = 'lightbox';
        document.getElementById('llmPromptInput').value = '';
        document.getElementById('llmPromptDialog').showModal();
    });
    document.getElementById('closeLlmPromptBtn').addEventListener('click', () => {
        document.getElementById('llmPromptDialog').close();
    });
    document.getElementById('cancelLlmPromptBtn').addEventListener('click', () => {
        document.getElementById('llmPromptDialog').close();
    });

    // Macro templates listener
    document.querySelectorAll('.btn-macro').forEach(btn => {
        btn.addEventListener('click', () => {
            const macroText = btn.getAttribute('data-macro');
            const textarea = document.getElementById('llmPromptInput');
            const percentIndex = macroText.indexOf('%');
            
            if (percentIndex !== -1) {
                const cleanText = macroText.replace('%', '');
                textarea.value = cleanText;
                textarea.focus();
                textarea.setSelectionRange(percentIndex, percentIndex);
            } else {
                textarea.value = macroText;
                textarea.focus();
                textarea.setSelectionRange(macroText.length, macroText.length);
            }
        });
    });

    // Confirm LLM transformation
    document.getElementById('confirmLlmPromptBtn').addEventListener('click', () => {
        const promptText = document.getElementById('llmPromptInput').value.trim();
        if (!promptText) {
            alert("Bitte gib eine Anweisung für die KI ein.");
            return;
        }
        
        let currentHtml = '';
        if (currentActiveEditor === 'tile') {
            currentHtml = (monacoHtmlEditorInstance && monacoLoaded)
                ? monacoHtmlEditorInstance.getValue()
                : document.getElementById('editHtmlContent').value;
        } else if (currentActiveEditor === 'lightbox') {
            currentHtml = (monacoLightboxEditorInstance && monacoLoaded)
                ? monacoLightboxEditorInstance.getValue()
                : document.getElementById('editLightboxHtmlContent').value;
        }
        
        const confirmBtn = document.getElementById('confirmLlmPromptBtn');
        const origHtml = confirmBtn.innerHTML;
        confirmBtn.disabled = true;
        confirmBtn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Generiere...';
        
        const formData = new FormData();
        formData.append('prompt', promptText);
        formData.append('html_teaser', currentHtml);
        
        fetch('admin.php?action=edit_html_with_llm', {
            method: 'POST',
            body: formData
        })
        .then(res => {
            if (!res.ok) throw new Error("Server error running LLM");
            return res.json();
        })
        .then(res => {
            if (res.status === 'success') {
                if (currentActiveEditor === 'tile') {
                    if (monacoHtmlEditorInstance && monacoLoaded) {
                        monacoHtmlEditorInstance.setValue(res.html_teaser || '');
                    } else {
                        document.getElementById('editHtmlContent').value = res.html_teaser || '';
                    }
                    updateLivePreview();
                } else if (currentActiveEditor === 'lightbox') {
                    if (monacoLightboxEditorInstance && monacoLoaded) {
                        monacoLightboxEditorInstance.setValue(res.html_teaser || '');
                    } else {
                        document.getElementById('editLightboxHtmlContent').value = res.html_teaser || '';
                    }
                }
                document.getElementById('llmPromptDialog').close();
            } else {
                alert("Fehler von der KI: " + res.message);
            }
        })
        .catch(err => {
            alert("Fehler bei der Kommunikation mit dem KI-Service.");
            console.error(err);
        })
        .finally(() => {
            confirmBtn.disabled = false;
            confirmBtn.innerHTML = origHtml;
        });
    });

    // Suggest Meta tags and summary click listener
    document.getElementById('suggestMetaBtn').addEventListener('click', () => {
        const name = document.getElementById('editName').value.trim();
        const title = document.getElementById('editTitle').value.trim();
        if (!name || !title) {
            alert("Bitte trage zuerst 'Name' und 'Anzeigetitel' ein, damit die KI das Dokument zuordnen kann.");
            return;
        }
        
        const contentFile = document.getElementById('editContentFile').value.trim();
        const htmlContent = (monacoHtmlEditorInstance && monacoLoaded)
            ? monacoHtmlEditorInstance.getValue()
            : document.getElementById('editHtmlContent').value;
            
        const btn = document.getElementById('suggestMetaBtn');
        const origHtml = btn.innerHTML;
        btn.disabled = true;
        btn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Analysiere...';
        
        const formData = new FormData();
        formData.append('name', name);
        formData.append('language', document.getElementById('editLanguage').value.trim());
        formData.append('title', title);
        formData.append('content_file', contentFile);
        formData.append('html_teaser', htmlContent);
        
        fetch('admin.php?action=suggest_meta', {
            method: 'POST',
            body: formData
        })
        .then(res => {
            if (!res.ok) throw new Error("Server error suggesting meta");
            return res.json();
        })
        .then(res => {
            if (res.status === 'success') {
                const summaryVal = res.data.summary || '';
                const tagsVal = Array.isArray(res.data.tags) ? res.data.tags.join(', ') : '';
                
                if (summaryVal) {
                    document.getElementById('editSummary').value = summaryVal;
                }
                if (tagsVal) {
                    document.getElementById('editCategoryTags').value = tagsVal;
                }
                formDirty = true;
            } else {
                alert("Fehler bei der KI-Generierung: " + res.message);
            }
        })
        .catch(err => {
            alert("Fehler bei der Verbindung zum KI-Service.");
            console.error(err);
        })
        .finally(() => {
            btn.disabled = false;
            btn.innerHTML = origHtml;
        });
    });

    // Image Picker dialog event bindings
    document.getElementById('openImagePickerBtn').addEventListener('click', openImagePicker);
    document.getElementById('closeImagePickerBtn').addEventListener('click', () => {
        document.getElementById('imagePickerDialog').close();
    });
    document.getElementById('cancelImagePickerBtn').addEventListener('click', () => {
        document.getElementById('imagePickerDialog').close();
    });
    document.getElementById('imageUploadInput').addEventListener('change', handleImageUpload);
}

// Translation Health State
let translationStatusMap = {};

function fetchTranslationStatuses() {
    if (!isAdmin) return;
    fetch('admin.php?action=translation_status')
        .then(res => res.json())
        .then(res => {
            if (res.status === 'success') {
                translationStatusMap = res.data || {};
                updateTranslationBadgesInGrid();
            }
        })
        .catch(err => console.error("Error fetching translation status:", err));
}

function renderTileBadge(wrapper, name) {
    const tileInfo = translationStatusMap[name];
    if (!tileInfo) return;

    wrapper.innerHTML = '';
    const langs = tileInfo.languages || {};
    
    Object.keys(langs).forEach(lCode => {
        const langData = langs[lCode];
        const span = document.createElement('span');
        span.className = `lang-badge ${langData.status}`;
        
        let icon = '';
        if (langData.is_source) icon = '<i class="fa-solid fa-crown"></i> ';
        else if (langData.status === 'stale') icon = '<i class="fa-solid fa-triangle-exclamation"></i> ';
        else icon = '<i class="fa-solid fa-check"></i> ';
        
        span.innerHTML = `${icon}${lCode.toUpperCase()}`;
        span.title = langData.is_source ? `Master-Sprache (${lCode.toUpperCase()})` : (langData.status === 'stale' ? `Übersetzung (${lCode.toUpperCase()}) ist veraltet!` : `Übersetzung (${lCode.toUpperCase()}) ist aktuell`);
        wrapper.appendChild(span);
    });
}

function updateTranslationBadgesInGrid() {
    document.querySelectorAll('.translation-badges').forEach(wrapper => {
        const tName = wrapper.dataset.tileName;
        if (tName) {
            renderTileBadge(wrapper, tName);
        }
    });
}

function triggerAutoTranslate() {
    const tileName = document.getElementById('editName').value.trim();

    if (!tileName) {
        alert("Bitte gib erst einen Kachelnamen an.");
        return;
    }

    const btn = document.getElementById('autoTranslateBtn');
    const origHtml = btn.innerHTML;
    btn.disabled = true;
    btn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Prüfe Status...';

    fetch(`admin.php?action=translation_status&name=${encodeURIComponent(tileName)}`)
        .then(res => res.json())
        .then(res => {
            let missingLangs = [];
            if (res.status === 'success' && res.data) {
                missingLangs = res.data.missing_languages || [];
            }

            let confirmMsg = '';
            if (missingLangs.length > 0) {
                confirmMsg = `Möchtest Du die Kachel "${tileName}" per KI in alle ${missingLangs.length} fehlenden Sprachen (${missingLangs.map(l => l.toUpperCase()).join(', ')}) übersetzen und speichern?\n\nHinweis: Das Übersetzungssystem gewährt dem KI-Modell ausreichend Ausführungszeit.`;
            } else {
                confirmMsg = `Alle unterstützten Sprachen existieren bereits für "${tileName}". Möchtest Du trotzdem eine Neuübersetzung für alle Zielsprachen durchführen?`;
            }

            if (!confirm(confirmMsg)) {
                btn.disabled = false;
                btn.innerHTML = origHtml;
                return;
            }

            btn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> KI Übersetzt...';

            const formData = new FormData();
            formData.append('name', tileName);
            formData.append('target_lang', 'all');

            return fetch('admin.php?action=auto_translate', {
                method: 'POST',
                body: formData
            })
                .then(res => res.json())
                .then(res => {
                    if (res.status === 'success') {
                        alert(res.message);
                        document.getElementById('editorDialog').close();
                        resetAndLoad();
                    } else {
                        alert(`Fehler bei der KI-Übersetzung: ${res.message}`);
                    }
                });
        })
        .catch(err => {
            alert("Fehler bei der Kommunikation mit dem Server.");
            console.error("Auto-translate error:", err);
        })
        .finally(() => {
            btn.disabled = false;
            btn.innerHTML = origHtml;
        });
}

// Expose admin control rendering for card grids
function renderAdminControls(tileDiv, tile) {
    const adminBar = document.createElement('div');
    adminBar.className = 'tile-admin-controls';

    // Translation badge wrapper
    const badgeWrapper = document.createElement('div');
    badgeWrapper.className = 'translation-badges';
    badgeWrapper.dataset.tileName = tile.name;
    badgeWrapper.dataset.tileLang = tile.language;
    adminBar.appendChild(badgeWrapper);
    renderTileBadge(badgeWrapper, tile.name);
    
    // Visibility toggle
    const viewIcon = tile.visible ? 'fa-eye-slash' : 'fa-eye';
    const viewTitle = tile.visible ? 'Ausblenden' : 'Einblenden';
    adminBar.innerHTML += `<button class="admin-icon-btn toggle-vis-btn" title="${viewTitle}"><i class="fa-solid ${viewIcon}"></i></button>`;
    
    // Edit btn
    adminBar.innerHTML += `<button class="admin-icon-btn edit-btn" title="Bearbeiten"><i class="fa-solid fa-pen"></i></button>`;
    
    // Clone btn
    adminBar.innerHTML += `<button class="admin-icon-btn clone-btn" title="Duplizieren (Klonen)"><i class="fa-solid fa-copy"></i></button>`;
    
    // Delete btn
    adminBar.innerHTML += `<button class="admin-icon-btn delete-btn" title="Löschen"><i class="fa-solid fa-trash"></i></button>`;
    
    tileDiv.appendChild(adminBar);
    
    // Admin actions listeners
    adminBar.querySelector('.toggle-vis-btn').addEventListener('click', (e) => {
        e.stopPropagation();
        toggleVisibility(tile.id);
    });
    
    adminBar.querySelector('.edit-btn').addEventListener('click', (e) => {
        e.stopPropagation();
        openEditor(tile);
    });
    
    adminBar.querySelector('.clone-btn').addEventListener('click', (e) => {
        e.stopPropagation();
        cloneTile(tile.id);
    });
    
    adminBar.querySelector('.delete-btn').addEventListener('click', (e) => {
        e.stopPropagation();
        deleteTile(tile.id, tile.title);
    });
}
window.renderAdminControls = renderAdminControls;


// Expose click-outside checks for admin dialogs
function handleAdminModalClose(e) {
    const editor = document.getElementById('editorDialog');
    const settings = document.getElementById('settingsDialog');
    const lightboxEditor = document.getElementById('lightboxEditorDialog');
    const llmPrompt = document.getElementById('llmPromptDialog');
    const imagePicker = document.getElementById('imagePickerDialog');
    if (e.target === editor) closeEditorWithConfirm();
    if (e.target === settings) closeSettingsWithConfirm();
    if (e.target === lightboxEditor) {
        lightboxEditor.close();
        window.isEditing = false;
    }
    if (e.target === llmPrompt) {
        llmPrompt.close();
    }
    if (e.target === imagePicker) {
        imagePicker.close();
    }
}
window.handleAdminModalClose = handleAdminModalClose;

// Open Lightbox Content Editor Dialog
function openLightboxEditor(tile) {
    window.isEditing = true;
    const dialog = document.getElementById('lightboxEditorDialog');
    document.getElementById('lightboxEditTileId').value = tile.id;
    document.getElementById('lightboxEditFileName').value = tile.content_file || '';
    
    document.getElementById('lightboxEditorTitle').textContent = tile.content_file
        ? `Dokument bearbeiten: content/${tile.content_file}`
        : `HTML-Inhalt bearbeiten: ${tile.title}`;
        
    const container = document.getElementById('monacoLightboxEditor');
    const textarea = document.getElementById('editLightboxHtmlContent');
    
    // Set loading
    textarea.value = 'Lade Inhalt...';
    if (monacoLightboxEditorInstance) {
        monacoLightboxEditorInstance.setValue('Lade Inhalt...');
    }
    
    let contentPromise;
    if (tile.content_file) {
        contentPromise = fetch(`admin.php?action=get_content_file&file=${encodeURIComponent(tile.content_file)}`)
            .then(res => {
                if (!res.ok) throw new Error("Network response error loading file");
                return res.json();
            })
            .then(res => {
                if (res.status === 'success') {
                    return res.content;
                }
                throw new Error(res.message);
            });
    } else {
        contentPromise = Promise.resolve(tile.html_teaser || '');
    }
    
    contentPromise.then(html => {
        textarea.value = html;
        lazyLoadMonaco(() => {
            textarea.style.display = 'none';
            container.style.display = 'block';
            
            if (!monacoLightboxEditorInstance) {
                monacoLightboxEditorInstance = monaco.editor.create(container, {
                    value: html,
                    language: 'html',
                    theme: document.body.classList.contains('light-theme') ? 'vs' : 'vs-dark',
                    minimap: { enabled: false },
                    fontSize: 12,
                    automaticLayout: true
                });
            } else {
                monacoLightboxEditorInstance.setValue(html);
                monaco.editor.setTheme(document.body.classList.contains('light-theme') ? 'vs' : 'vs-dark');
            }
        });
    }).catch(err => {
        alert("Fehler beim Laden des Inhalts: " + err.message);
        textarea.value = '';
    });
    
    dialog.showModal();
}
window.openLightboxEditor = openLightboxEditor;

// Save Lightbox Content
function saveLightboxEditor() {
    const id = document.getElementById('lightboxEditTileId').value;
    const file = document.getElementById('lightboxEditFileName').value;
    
    const htmlContent = (monacoLightboxEditorInstance && monacoLoaded)
        ? monacoLightboxEditorInstance.getValue()
        : document.getElementById('editLightboxHtmlContent').value;
        
    const saveBtn = document.getElementById('saveLightboxEditorBtn');
    const origHtml = saveBtn.innerHTML;
    saveBtn.disabled = true;
    saveBtn.innerHTML = '<i class="fa-solid fa-spinner fa-spin"></i> Speichere...';
    
    const formData = new FormData();
    let url = '';
    
    if (file) {
        url = 'admin.php?action=save_content_file';
        formData.append('file', file);
        formData.append('content', htmlContent);
    } else {
        url = 'admin.php?action=save_tile_html';
        formData.append('id', id);
        formData.append('html_teaser', htmlContent);
    }
    
    fetch(url, {
        method: 'POST',
        body: formData
    })
    .then(res => {
        if (!res.ok) throw new Error("Save error");
        return res.json();
    })
    .then(res => {
        if (res.status === 'success') {
            document.getElementById('lightboxEditorDialog').close();
            window.isEditing = false;
            
            // Refresh lightbox
            if (window.refreshActiveLightbox) {
                window.refreshActiveLightbox();
            }
        } else {
            alert("Fehler beim Speichern: " + res.message);
        }
    })
    .catch(err => {
        alert("Fehler beim Kommunizieren mit dem Server.");
        console.error("Save error:", err);
    })
    .finally(() => {
        saveBtn.disabled = false;
        saveBtn.innerHTML = origHtml;
    });
}

// Render edit button inside lightbox details view
function renderLightboxEditButton(tile, targetBody) {
    if (!isAdmin) return;
    
    const editContainer = document.createElement('div');
    editContainer.className = 'lightbox-admin-container';
    editContainer.style.marginTop = '1.5rem';
    editContainer.style.display = 'flex';
    editContainer.style.justifyContent = 'flex-end';
    
    const editBtn = document.createElement('button');
    editBtn.className = 'btn-admin-edit';
    editBtn.innerHTML = '<i class="fa-solid fa-pen-to-square"></i> Inhalt bearbeiten';
    editBtn.style.padding = '0.5rem 1rem';
    editBtn.style.fontSize = '0.9rem';
    editBtn.addEventListener('click', () => {
        openLightboxEditor(tile);
    });
    
    editContainer.appendChild(editBtn);
    targetBody.appendChild(editContainer);
}
window.renderLightboxEditButton = renderLightboxEditButton;

// Register page exit warning for dirty admin edits
window.addEventListener('beforeunload', (e) => {
    if (window.isEditing || window.isSettingsEditing) {
        e.preventDefault();
        e.returnValue = 'Änderungen werden eventuell nicht gespeichert.';
        return 'Änderungen werden eventuell nicht gespeichert.';
    }
});

// Expose initAdmin globally so it can be called from app.js once HTML is injected
window.initAdmin = initAdmin;

// -------------------------------------------------------------
// IMAGE PICKER / MEDIATHEK DIALOG WORKFLOW
// -------------------------------------------------------------

function openImagePicker() {
    const dialog = document.getElementById('imagePickerDialog');
    loadImagePickerFiles();
    dialog.showModal();
}

function loadImagePickerFiles() {
    const grid = document.getElementById('imagePickerGrid');
    grid.innerHTML = '<div style="grid-column: 1/-1; text-align: center; padding: 2rem;"><i class="fa-solid fa-spinner fa-spin" style="font-size: 2rem; color: var(--accent);"></i></div>';
    
    fetch('admin.php?action=list_images')
        .then(res => res.json())
        .then(res => {
            if (res.status !== 'success') {
                throw new Error(res.message || "Failed to load images");
            }
            
            grid.innerHTML = '';
            
            // 1. Render the Upload Button Card first
            const uploadCard = document.createElement('div');
            uploadCard.className = 'image-picker-item upload-btn-card';
            uploadCard.id = 'uploadBtnCard';
            uploadCard.innerHTML = `
                <i class="fa-solid fa-cloud-arrow-up"></i>
                <span style="font-size: 0.8rem; margin-top: 0.5rem; font-weight:600;">Bild hochladen</span>
            `;
            uploadCard.addEventListener('click', () => {
                document.getElementById('imageUploadInput').click();
            });
            grid.appendChild(uploadCard);
            
            // 2. Render each image item
            res.images.forEach(img => {
                const item = document.createElement('div');
                item.className = 'image-picker-item';
                item.setAttribute('data-name', img.name);
                
                item.innerHTML = `
                    <img src="${img.url}?v=${Date.now()}" alt="${img.name}">
                    <div class="image-item-overlay">
                        <button type="button" class="image-action-btn rename-btn" title="Umbenennen"><i class="fa-solid fa-pen"></i></button>
                        <button type="button" class="image-action-btn delete-btn" title="Löschen"><i class="fa-solid fa-trash"></i></button>
                    </div>
                    <div class="image-item-name" title="${img.name}">${img.name}</div>
                `;
                
                // Select image behavior (on clicking the card itself, except overlay)
                item.addEventListener('click', (e) => {
                    if (e.target.closest('.image-action-btn')) {
                        return; // let buttons handle it
                    }
                    // Insert CSS background rule
                    document.getElementById('editBackground').value = `url(./tileimg/${img.name}) center/cover`;
                    formDirty = true;
                    updateLivePreview();
                    document.getElementById('imagePickerDialog').close();
                });
                
                // Rename button listener
                item.querySelector('.rename-btn').addEventListener('click', (e) => {
                    e.stopPropagation();
                    renameImage(img.name);
                });
                
                // Delete button listener
                item.querySelector('.delete-btn').addEventListener('click', (e) => {
                    e.stopPropagation();
                    deleteImage(img.name);
                });
                
                grid.appendChild(item);
            });
        })
        .catch(err => {
            grid.innerHTML = `<div style="grid-column: 1/-1; color: var(--danger); text-align: center; padding: 2rem;">Laden fehlgeschlagen: ${err.message}</div>`;
        });
}

// Upload file handler
function handleImageUpload(e) {
    const fileInput = e.target;
    if (!fileInput.files || fileInput.files.length === 0) return;
    
    const file = fileInput.files[0];
    const formData = new FormData();
    formData.append('image', file);
    
    const uploadCard = document.getElementById('uploadBtnCard');
    const originalHtml = uploadCard.innerHTML;
    uploadCard.innerHTML = '<i class="fa-solid fa-spinner fa-spin" style="font-size: 2rem; color: var(--accent);"></i><span style="font-size: 0.8rem; margin-top: 0.5rem;">Verarbeite...</span>';
    uploadCard.style.pointerEvents = 'none';
    
    fetch('admin.php?action=upload_image', {
        method: 'POST',
        body: formData
    })
    .then(res => res.json())
    .then(res => {
        if (res.status === 'success') {
            // Success! Refresh list and prompt user to rename it
            loadImagePickerFiles();
            
            // Delay slightly so the refreshed list starts loading
            setTimeout(() => {
                const initialName = res.image.name;
                const newNameInput = prompt("Bild erfolgreich hochgeladen! Möchtest du die Datei umbenennen?", initialName);
                if (newNameInput && newNameInput.trim() !== '' && newNameInput !== initialName) {
                    performRenameImageAction(initialName, newNameInput.trim());
                }
            }, 300);
        } else {
            alert("Upload fehlgeschlagen: " + res.message);
            loadImagePickerFiles();
        }
    })
    .catch(err => {
        alert("Fehler beim Hochladen des Bildes.");
        console.error(err);
        loadImagePickerFiles();
    })
    .finally(() => {
        fileInput.value = ''; // clear input selection
    });
}

// Rename image prompt workflow
function renameImage(filename) {
    const newName = prompt(`Datei "${filename}" umbenennen in:`, filename);
    if (!newName || newName.trim() === '' || newName === filename) {
        return;
    }
    performRenameImageAction(filename, newName.trim());
}

function performRenameImageAction(oldName, newName) {
    const formData = new FormData();
    formData.append('old_name', oldName);
    formData.append('new_name', newName);
    
    fetch('admin.php?action=rename_image', {
        method: 'POST',
        body: formData
    })
    .then(res => res.json())
    .then(res => {
        if (res.status === 'success') {
            loadImagePickerFiles();
        } else {
            alert("Umbenennen fehlgeschlagen: " + res.message);
        }
    })
    .catch(err => {
        alert("Fehler beim Kommunizieren mit dem Server.");
        console.error(err);
    });
}

// Delete image workflow
function deleteImage(filename) {
    if (!confirm(`Soll das Bild "${filename}" wirklich unwiderruflich aus der Mediathek gelöscht werden?`)) {
        return;
    }
    
    const formData = new FormData();
    formData.append('name', filename);
    
    fetch('admin.php?action=delete_image', {
        method: 'POST',
        body: formData
    })
    .then(res => res.json())
    .then(res => {
        if (res.status === 'success') {
            loadImagePickerFiles();
        } else {
            alert("Löschen fehlgeschlagen: " + res.message);
        }
    })
    .catch(err => {
        alert("Fehler beim Kommunizieren mit dem Server.");
        console.error(err);
    });
}
