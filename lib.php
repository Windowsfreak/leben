<?php
require_once __DIR__ . '/config.php';

// Get list of supported languages from config.json
function get_supported_languages() {
    static $langs = null;
    if ($langs === null) {
        $config_file = __DIR__ . '/config.json';
        if (file_exists($config_file)) {
            $json = json_decode(file_get_contents($config_file), true);
            if (!empty($json['supported_languages']) && is_array($json['supported_languages'])) {
                $langs = $json['supported_languages'];
            }
        }
        if (empty($langs)) {
            $langs = ['de' => 'Deutsch', 'en' => 'English'];
        }
    }
    return $langs;
}

// Get PostgreSQL Database Connection
function get_db_connection() {
    static $pdo = null;
    if ($pdo === null) {
        $dsn = sprintf("pgsql:host=%s;port=%s;dbname=%s", DB_HOST, DB_PORT, DB_NAME);
        try {
            $pdo = new PDO($dsn, DB_USER, DB_PASS, [
                PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
                PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
                PDO::ATTR_EMULATE_PREPARES => false,
            ]);
        } catch (PDOException $e) {
            throw new Exception("Database connection failed: " . $e->getMessage());
        }
    }
    return $pdo;
}

// Generate Vector Embedding from Ollama
function get_embedding($text, $type = 'document') {
    $url = EMBEDDING_URL;
    $model = EMBEDDING_MODEL;
    
    // Add appropriate prefix for nomic-embed-text
    $prefix = ($type === 'query') ? EMBEDDING_QUERY_PREFIX : EMBEDDING_DOC_PREFIX;
    $input_text = $prefix . $text;

    $ch = curl_init("$url/api/embed");
    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode([
        'model' => $model,
        'input' => $input_text
    ]));
    curl_setopt($ch, CURLOPT_HTTPHEADER, ['Content-Type: application/json']);
    curl_setopt($ch, CURLOPT_TIMEOUT, 10);
    $response = curl_exec($ch);
    $status = curl_getinfo($ch, CURLINFO_HTTP_CODE);

    if ($status === 200 && $response) {
        $json = json_decode($response, true);
        if (isset($json['embeddings'][0])) {
            return $json['embeddings'][0];
        }
    }

    throw new Exception("Failed to get embedding from Ollama (Status: $status). Response: " . substr($response, 0, 500));
}

// Convert float array to PostgreSQL vector literal format: [0.1,0.2,...]
function array_to_postgres_vector($array) {
    if (!is_array($array)) {
        return null;
    }
    return '[' . implode(',', array_map('floatval', $array)) . ']';
}

// Format tile document text for embedding alignment
function format_tile_document_text($name, $language, $tags, $summary) {
    // If $tags is string (e.g. from Postgres, or comma-separated), clean and parse it
    if (is_string($tags)) {
        $tags = trim($tags, '{}');
        $tags_array = array_filter(array_map('trim', explode(',', $tags)));
    } else {
        $tags_array = (array)$tags;
    }
    $tags_str = implode(' ', $tags_array);
    return "{$name} " . strtoupper($language) . ", {$tags_str}: {$summary}";
}

// Extract reference access codes sent by frontend header (e.g. X-Reference: code1,code2)
function get_request_reference_codes() {
    $headers = [];
    if (function_exists('getallheaders')) {
        $headers = array_change_key_case(getallheaders() ?: [], CASE_LOWER);
    }
    $ref_str = '';
    if (isset($headers['x-reference'])) {
        $ref_str = $headers['x-reference'];
    } elseif (isset($_SERVER['HTTP_X_REFERENCE'])) {
        $ref_str = $_SERVER['HTTP_X_REFERENCE'];
    }
    if (empty($ref_str)) {
        return [];
    }
    $parts = explode(',', $ref_str);
    $cleaned = [];
    foreach ($parts as $p) {
        $p = trim($p);
        if ($p !== '') {
            $cleaned[] = $p;
        }
    }
    return array_unique($cleaned);
}

// Perform a language-resolved query (with optional semantic search)
function search_tiles($pref_lang, $query_string = null, $offset = 0, $limit = 20) {
    $db = get_db_connection();
    
    // If limit is 0 or less, treat as no limit (unlimited)
    $db_limit = ($limit <= 0) ? 999999 : (int)$limit;

    // Check if administrator is logged in to bypass visibility check
    $show_invisible = false;
    if (session_status() === PHP_SESSION_NONE) {
        session_start();
    }
    if (!empty($_SESSION['admin_logged_in'])) {
        $show_invisible = true;
    }

    $ref_codes = get_request_reference_codes();
    $ref_codes_str = implode(',', $ref_codes);
    
    if (!empty($query_string)) {
        // Append language code in all-caps to target alignment with document embedding format
        $search_text = $query_string . ' ' . strtoupper($pref_lang);
        
        // Embed the query using query prefix
        $query_vector = get_embedding($search_text, 'query');
        $vector_str = array_to_postgres_vector($query_vector);
        
        $sql = "
            WITH resolved_tiles AS (
                SELECT *,
                       (embedding <=> :vector::vector) as distance,
                       ROW_NUMBER() OVER (
                           PARTITION BY name 
                           ORDER BY 
                               CASE WHEN language = :pref_lang THEN 1 
                                    ELSE 2 
                               END,
                               (embedding <=> :vector::vector) ASC
                       ) as rn
                FROM tiles
                WHERE (:show_invisible = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array(:ref_codes, ',')))))
            )
            SELECT id, name, language, tags, title, html_teaser, 
                   summary, link, type, content_file, 
                   visible, accent_color, background, created_at, updated_at, distance
            FROM resolved_tiles 
            WHERE rn = 1 
            ORDER BY distance ASC 
            LIMIT :limit OFFSET :offset
        ";
        
        $stmt = $db->prepare($sql);
        $stmt->bindValue(':vector', $vector_str, PDO::PARAM_STR);
        $stmt->bindValue(':pref_lang', $pref_lang, PDO::PARAM_STR);
        $stmt->bindValue(':show_invisible', $show_invisible, PDO::PARAM_BOOL);
        $stmt->bindValue(':ref_codes', $ref_codes_str, PDO::PARAM_STR);
        $stmt->bindValue(':limit', (int)$db_limit, PDO::PARAM_INT);
        $stmt->bindValue(':offset', (int)$offset, PDO::PARAM_INT);
        $stmt->execute();
        return $stmt->fetchAll();
    } else {
        // No search query: order by sort_order / default sorting
        $sql = "
            WITH resolved_tiles AS (
                SELECT *,
                       ROW_NUMBER() OVER (
                           PARTITION BY name 
                           ORDER BY 
                               CASE WHEN language = :pref_lang THEN 1 
                                    ELSE 2 
                               END,
                               sort_order ASC, created_at DESC
                       ) as rn
                FROM tiles
                WHERE (:show_invisible = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array(:ref_codes, ',')))))
            )
            SELECT id, name, language, tags, title, html_teaser, 
                   summary, link, type, content_file, 
                   visible, accent_color, background, created_at, updated_at, sort_order
            FROM resolved_tiles 
            WHERE rn = 1 
            ORDER BY sort_order ASC, created_at DESC
            LIMIT :limit OFFSET :offset
        ";
        
        $stmt = $db->prepare($sql);
        $stmt->bindValue(':pref_lang', $pref_lang, PDO::PARAM_STR);
        $stmt->bindValue(':show_invisible', $show_invisible, PDO::PARAM_BOOL);
        $stmt->bindValue(':ref_codes', $ref_codes_str, PDO::PARAM_STR);
        $stmt->bindValue(':limit', (int)$db_limit, PDO::PARAM_INT);
        $stmt->bindValue(':offset', (int)$offset, PDO::PARAM_INT);
        $stmt->execute();
        return $stmt->fetchAll();
    }
}

// Find similar tiles for the "See Also" section (language-resolved)
function get_similar_tiles($name, $pref_lang, $limit = 3, $offset = 0) {
    $db = get_db_connection();
    $db_limit = ($limit <= 0) ? 999999 : (int)$limit;

    $show_invisible = false;
    if (session_status() === PHP_SESSION_NONE) {
        session_start();
    }
    if (!empty($_SESSION['admin_logged_in'])) {
        $show_invisible = true;
    }

    $ref_codes = get_request_reference_codes();
    $ref_codes_str = implode(',', $ref_codes);

    $sql = "
        WITH source_tile AS (
            SELECT embedding 
            FROM tiles 
            WHERE name = :name 
            ORDER BY CASE WHEN language = :pref_lang THEN 1 ELSE 2 END, created_at ASC
            LIMIT 1
        ),
        resolved_tiles AS (
            SELECT *,
                   (embedding <=> (SELECT embedding FROM source_tile)) as distance,
                   ROW_NUMBER() OVER (
                       PARTITION BY name 
                       ORDER BY 
                           CASE WHEN language = :pref_lang THEN 1 
                                ELSE 2 
                           END,
                           (embedding <=> (SELECT embedding FROM source_tile)) ASC
                       ) as rn
            FROM tiles
            WHERE (:show_invisible = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array(:ref_codes, ','))))) AND name != :name
        )
        SELECT id, name, language, tags, title, html_teaser, 
               summary, link, type, content_file, 
               visible, accent_color, background, created_at, updated_at, distance
        FROM resolved_tiles 
        WHERE rn = 1 AND (SELECT embedding FROM source_tile) IS NOT NULL
        ORDER BY distance ASC 
        LIMIT :limit OFFSET :offset
    ";

    $stmt = $db->prepare($sql);
    $stmt->bindValue(':name', $name, PDO::PARAM_STR);
    $stmt->bindValue(':pref_lang', $pref_lang, PDO::PARAM_STR);
    $stmt->bindValue(':show_invisible', $show_invisible, PDO::PARAM_BOOL);
    $stmt->bindValue(':ref_codes', $ref_codes_str, PDO::PARAM_STR);
    $stmt->bindValue(':limit', (int)$db_limit, PDO::PARAM_INT);
    $stmt->bindValue(':offset', (int)$offset, PDO::PARAM_INT);
    $stmt->execute();
    return $stmt->fetchAll();
}

// Call local LLM completions / chat completions endpoint
function call_llm($system_prompt, $user_content) {
    $url = LLM_URL;
    if (strpos($url, '/chat/completions') === false && strpos($url, '/completions') === false) {
        $url = rtrim($url, '/') . '/v1/chat/completions';
    }

    $ch = curl_init($url);
    $headers = [
        'Content-Type: application/json'
    ];
    if (defined('LLM_PASS') && LLM_PASS !== '') {
        if (defined('LLM_USER') && LLM_USER !== '') {
            $headers[] = 'Authorization: Basic ' . base64_encode(LLM_USER . ':' . LLM_PASS);
        } else {
            $headers[] = 'Authorization: Bearer ' . LLM_PASS;
        }
    }

    $data = [
        'model' => LLM_MODEL,
        'messages' => [
            ['role' => 'system', 'content' => $system_prompt],
            ['role' => 'user', 'content' => $user_content]
        ],
        'temperature' => 0.3
    ];

    curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
    curl_setopt($ch, CURLOPT_POST, true);
    curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode($data));
    curl_setopt($ch, CURLOPT_HTTPHEADER, $headers);
    curl_setopt($ch, CURLOPT_TIMEOUT, 60);
    $response = curl_exec($ch);
    $status = curl_getinfo($ch, CURLINFO_HTTP_CODE);

    if ($status === 200 && $response) {
        $json = json_decode($response, true);
        if (isset($json['choices'][0]['message']['content'])) {
            return $json['choices'][0]['message']['content'];
        }
    }

    throw new Exception("LLM call failed (Status: $status). Response: " . substr($response, 0, 500));
}

// Determine translation health and source of truth for a given tile name or all tiles
function get_translation_status($tile_name = null) {
    $db = get_db_connection();
    
    if ($tile_name) {
        $stmt = $db->prepare("SELECT * FROM tiles WHERE name = :name ORDER BY created_at ASC");
        $stmt->execute([':name' => $tile_name]);
        $rows = $stmt->fetchAll();
    } else {
        $stmt = $db->prepare("SELECT * FROM tiles ORDER BY name ASC, created_at ASC");
        $stmt->execute();
        $rows = $stmt->fetchAll();
    }

    if (empty($rows)) {
        return [];
    }

    // Group rows by tile name
    $grouped = [];
    foreach ($rows as $row) {
        $grouped[$row['name']][] = $row;
    }

    $results = [];
    $contents_dir = __DIR__ . '/content/';
    $all_supported = array_keys(get_supported_languages());

    foreach ($grouped as $name => $siblings) {
        // Oldest created_at sibling is the source of truth
        $source_row = $siblings[0];
        $source_lang = $source_row['language'];
        $source_db_mtime = strtotime($source_row['updated_at']);

        // Check source content file mtime if present
        $source_file_mtime = 0;
        if (!empty($source_row['content_file'])) {
            $source_file_path = $contents_dir . $source_row['content_file'];
            if (file_exists($source_file_path)) {
                $source_file_mtime = filemtime($source_file_path);
            }
        }
        $effective_source_mtime = max($source_db_mtime, $source_file_mtime);

        $lang_matrix = [];
        $has_stale = false;

        foreach ($siblings as $sibling) {
            $s_lang = $sibling['language'];
            $is_source = ($s_lang === $source_lang);

            $s_db_mtime = strtotime($sibling['updated_at']);
            $s_file_mtime = 0;
            if (!empty($sibling['content_file'])) {
                $s_file_path = $contents_dir . $sibling['content_file'];
                if (file_exists($s_file_path)) {
                    $s_file_mtime = filemtime($s_file_path);
                }
            }

            $status = 'up_to_date';
            if ($is_source) {
                $status = 'source';
            } else if ($s_db_mtime < $effective_source_mtime) {
                $status = 'stale';
                $has_stale = true;
            }

            $lang_matrix[$s_lang] = [
                'id' => (int)$sibling['id'],
                'language' => $s_lang,
                'is_source' => $is_source,
                'status' => $status,
                'created_at' => $sibling['created_at'],
                'updated_at' => $sibling['updated_at'],
                'content_file' => $sibling['content_file'],
                'file_mtime' => $s_file_mtime ? date('Y-m-d H:i:s', $s_file_mtime) : null
            ];
        }

        $present_langs = array_keys($lang_matrix);
        $missing_langs = array_values(array_diff($all_supported, $present_langs));

        $results[$name] = [
            'name' => $name,
            'source_language' => $source_lang,
            'effective_source_mtime' => date('Y-m-d H:i:s', $effective_source_mtime),
            'has_stale_translation' => $has_stale,
            'languages' => $lang_matrix,
            'missing_languages' => $missing_langs
        ];
    }

    return $tile_name ? ($results[$tile_name] ?? null) : $results;
}

// Execute auto translation for a given tile and target language(s)
function execute_auto_translate($name, $target_lang = 'all') {
    $db = get_db_connection();
    $name = trim($name);
    if (empty($name)) {
        throw new Exception("Tile name is required for translation.");
    }

    $stmt = $db->prepare("SELECT * FROM tiles WHERE name = :name ORDER BY created_at ASC LIMIT 1");
    $stmt->execute([':name' => $name]);
    $source_tile = $stmt->fetch();

    if (!$source_tile) {
        throw new Exception("Source card '{$name}' not found.");
    }

    $source_lang = $source_tile['language'];
    $supported_map = get_supported_languages();

    $target_langs = [];
    if ($target_lang === 'all' || $target_lang === 'missing') {
        $status = get_translation_status($name);
        $missing = $status['missing_languages'] ?? [];
        if (!empty($missing)) {
            $target_langs = $missing;
        } else {
            $target_langs = array_values(array_diff(array_keys($supported_map), [$source_lang]));
        }
    } else {
        $target_lang = strtolower(trim($target_lang));
        if (!array_key_exists($target_lang, $supported_map)) {
            throw new Exception("Target language '{$target_lang}' is not supported.");
        }
        if ($target_lang === $source_lang) {
            throw new Exception("Target language ('{$target_lang}') is identical to source language ('{$source_lang}').");
        }
        $target_langs = [$target_lang];
    }

    $contents_dir = __DIR__ . '/content/';
    $raw_tags = trim($source_tile['tags'] ?? '', '{}');
    $tags_arr = array_filter(array_map('trim', explode(',', $raw_tags)));

    $results = [];

    foreach ($target_langs as $t_lang) {
        if ($t_lang === $source_lang) {
            continue;
        }

        $lang_full_name = $supported_map[$t_lang] ?? $t_lang;

        // 1. Translate metadata
        $meta_prompt = "You are an expert content translator. Translate the following tile metadata from " . strtoupper($source_lang) . " into {$lang_full_name}.
Respond ONLY with a valid JSON object (no markdown, no backticks):
{
  \"title\": \"Translated Title\",
  \"summary\": \"Translated summary...\",
  \"tags\": [\"tag1\", \"tag2\"]
}";
        $meta_user = "Source Title: {$source_tile['title']}\nSource Tags: " . implode(', ', $tags_arr) . "\nSource Summary: {$source_tile['summary']}";

        $meta_response = trim(call_llm($meta_prompt, $meta_user));
        if (strpos($meta_response, '```') !== false) {
            $meta_response = trim(preg_replace('/^```(?:json)?|```$/m', '', $meta_response));
        }
        $translated_meta = json_decode($meta_response, true);
        if (json_last_error() !== JSON_ERROR_NONE) {
            throw new Exception("LLM metadata translation failed for {$t_lang}: " . $meta_response);
        }

        $html_prompt = "You are an expert HTML translator. Translate all human-readable text in the provided HTML snippet into {$lang_full_name}.
Keep all HTML tags, structure, classes, IDs, icons (<i class=\"...\"></i>), and attributes intact.
Respond ONLY with the translated HTML string. Do not include markdown code block formatting (no backticks like ```html).";

        // 2. Translate teaser separately
        $translated_teaser = '';
        if (!empty($source_tile['html_teaser'])) {
            $res = trim(call_llm($html_prompt, $source_tile['html_teaser']));
            if (strpos($res, '```') !== false) {
                $res = trim(preg_replace('/^```(?:html|xml|json)?|```$/m', '', $res));
            }
            $translated_teaser = $res;
        }

        // 3. Translate content file separately if present
        $target_content_file = null;
        if (!empty($source_tile['content_file'])) {
            $fpath = $contents_dir . $source_tile['content_file'];
            if (file_exists($fpath)) {
                $source_file_content = file_get_contents($fpath);
                $translated_file_content = trim(call_llm($html_prompt, $source_file_content));
                if (strpos($translated_file_content, '```') !== false) {
                    $translated_file_content = trim(preg_replace('/^```(?:html|xml|json)?|```$/m', '', $translated_file_content));
                }
                $base_file = pathinfo($source_tile['content_file'], PATHINFO_FILENAME);
                $base_clean = preg_replace('/[_\-]([a-z]{2})$/i', '', $base_file);
                $target_content_file = "{$base_clean}_{$t_lang}.html";
                file_put_contents($contents_dir . $target_content_file, $translated_file_content);
            }
        }

        $new_tags = $translated_meta['tags'] ?? $tags_arr;
        $new_summary = $translated_meta['summary'] ?? $source_tile['summary'];
        $pg_tags = '{' . implode(',', array_map('trim', $new_tags)) . '}';

        $doc_text = format_tile_document_text($name, $t_lang, $new_tags, $new_summary);
        $vector_str = null;
        try {
            $embedding = get_embedding($doc_text, 'document');
            $vector_str = array_to_postgres_vector($embedding);
        } catch (Exception $e) {
            $vector_str = array_to_postgres_vector(array_fill(0, 768, 0.0));
        }

        $check_stmt = $db->prepare("SELECT id FROM tiles WHERE name = :name AND language = :target_lang");
        $check_stmt->execute([':name' => $name, ':target_lang' => $t_lang]);
        $existing_target = $check_stmt->fetch();

        if ($existing_target) {
            $sql = "
                UPDATE tiles 
                SET tags = :tags,
                    title = :title,
                    html_teaser = :html_teaser,
                    summary = :summary,
                    link = :link,
                    type = :type,
                    content_file = :content_file,
                    visible = :visible,
                    secret = :secret,
                    accent_color = :accent_color,
                    background = :background,
                    embedding = :embedding::vector,
                    sort_order = :sort_order,
                    updated_at = CURRENT_TIMESTAMP
                WHERE id = :id
            ";
            $stmt = $db->prepare($sql);
            $stmt->bindValue(':id', $existing_target['id'], PDO::PARAM_INT);
        } else {
            $sql = "
                INSERT INTO tiles (
                    name, language, tags, title, html_teaser,
                    summary, link, type, content_file,
                    visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
                ) VALUES (
                    :name, :language, :tags, :title, :html_teaser,
                    :summary, :link, :type, :content_file,
                    :visible, :secret, :accent_color, :background, :embedding::vector, :sort_order,
                    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
                )
            ";
            $stmt = $db->prepare($sql);
            $stmt->bindValue(':name', $name, PDO::PARAM_STR);
            $stmt->bindValue(':language', $t_lang, PDO::PARAM_STR);
        }
        $stmt->bindValue(':tags', $pg_tags, PDO::PARAM_STR);
        $stmt->bindValue(':title', $translated_meta['title'], PDO::PARAM_STR);
        $stmt->bindValue(':html_teaser', $translated_teaser, PDO::PARAM_STR);
        $stmt->bindValue(':summary', $new_summary, PDO::PARAM_STR);
        $stmt->bindValue(':link', $source_tile['link'], PDO::PARAM_STR);
        $stmt->bindValue(':type', $source_tile['type'], PDO::PARAM_STR);
        $stmt->bindValue(':content_file', $target_content_file, PDO::PARAM_STR);
        $stmt->bindValue(':visible', (bool)$source_tile['visible'], PDO::PARAM_BOOL);
        $stmt->bindValue(':secret', $source_tile['secret'] ?? '', PDO::PARAM_STR);
        $stmt->bindValue(':accent_color', $source_tile['accent_color'], PDO::PARAM_STR);
        $stmt->bindValue(':background', $source_tile['background'], PDO::PARAM_STR);
        $stmt->bindValue(':embedding', $vector_str, PDO::PARAM_STR);
        $stmt->bindValue(':sort_order', (int)$source_tile['sort_order'], PDO::PARAM_INT);

        $stmt->execute();

        $results[] = [
            'language' => $t_lang,
            'title' => $translated_meta['title'],
            'content_file' => $target_content_file
        ];
    }

    return [
        'name' => $name,
        'source_language' => $source_lang,
        'translated' => $results
    ];
}




